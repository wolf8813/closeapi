/*
Package controller 数据库同步控制器

功能特性：
- 双MySQL数据库频道表定时同步（每小时6次）
- 原子事务操作保证数据一致性
- 连接池管理及健康检查

组成结构：
+ StartChannelSync  服务入口
+ syncChannels      核心同步逻辑
+ atomicUpdate      事务管理
+ initDBConnection  连接池初始化

依赖：
- MySQL驱动 database/sql
- 定时任务 time
- 日志组件 common

环境要求：
1. 配置数据库连接参数（root/密码/地址/库名）
2. channels表需包含id,name字段
3. 数据库账号需CRUD权限
*/
package controller

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"one-api/common"
)

type Channel struct {
	Id   int            `json:"id"`
	Name sql.NullString `json:"name"`
}

// getMySQLDSN 根据数据库类型生成MySQL连接字符串
// 参数 dbType: 数据库标识（A/B）
// 返回值: MySQL DSN连接字符串
func getMySQLDSN(dbType string) string {
	switch dbType {
	case "A":
		return fmt.Sprintf("%s:%s@tcp(%s)/%s",
			"root",
			"new.1234",
			"127.0.0.1:3306",
			"mysql")
	case "B":
		return fmt.Sprintf("%s:%s@tcp(%s)/%s",
			"root",
			"yeqiu669.",
			"38.147.104.170:3366",
			"new-api")
	default:
		return ""
	}
}

// StartChannelSync 启动定时同步服务
// 功能说明：
// 1. 初始化双数据库连接（A/B两个数据源）
// 2. 创建10分钟间隔的定时器
// 3. 启动无限循环执行同步任务
func StartChannelSync() {
	// 初始化主数据库连接（数据库A）
	dbA := initDBConnection(getMySQLDSN("A"))
	// 初始化备用数据库连接（数据库B）
	dbB := initDBConnection(getMySQLDSN("B"))
	defer dbA.Close() // 确保程序退出时释放数据库连接
	defer dbB.Close() // 确保程序退出时释放数据库连接

	for {
		now := time.Now()
		// 计算到下一个整10分钟的时间间隔
		next := now.Truncate(10 * time.Minute).Add(10 * time.Minute)
		// 计算等待时长：下一个触发时间点与当前时间的差值
		waitDuration := next.Sub(now)
		//日志记录now, next, waitDuration
		common.SysLog(fmt.Sprintf("[ChannelSync] 当前时间: %s, 下次同步时间: %s, 等待时间: %s",
			now.Format("2006-01-02 15:04:05"), next.Format("2006-01-02 15:04:05"), waitDuration))

		time.Sleep(waitDuration)

		// 执行同步任务
		syncChannels(dbA, dbB)
	}
}

// syncChannels 执行双数据库同步的核心逻辑
// 参数：
//
//	dbA - 主数据库连接对象
//	dbB - 备数据库连接对象
//
// 功能流程：
// 1. 记录同步开始时间并生成启动日志
// 2. 从双数据库获取channels表数据
// 3. 执行原子性数据更新操作
// 4. 根据同步结果记录成功/失败日志
func syncChannels(dbA, dbB *sql.DB) {
	// 记录同步任务启动时间
	startTime := time.Now()
	common.SysLog(fmt.Sprintf("[ChannelSync] 开始同步 channels 表 (%s)", startTime.Format("2006-01-02 15:04:05")))

	// 从数据库A获取当前channels数据
	channelsA, err := getChannels(dbA)
	if err != nil {
		common.SysError(fmt.Sprintf("获取MySQL-A数据失败: %v", err)) // 记录致命错误日志
		return
	}

	// 从数据库B获取最新channels数据
	channelsB, err := getChannels(dbB)
	if err != nil {
		common.SysError(fmt.Sprintf("获取MySQL-B数据失败: %v", err))
		return
	}

	// 执行原子事务更新（先删后增）
	if err := atomicUpdate(dbA, channelsA, channelsB); err != nil {
		common.SysError(fmt.Sprintf("同步失败: %v", err)) // 更新失败日志
	} else {
		// 计算并记录同步耗时（精确到毫秒）
		common.SysLog(fmt.Sprintf("[ChannelSync] 同步完成，耗时 %v", time.Since(startTime).Round(time.Millisecond)))
	}
}

// atomicUpdate 执行原子性数据库更新操作
// 参数：
//
//	db - 目标数据库连接（需要执行更新的数据库）
//	a - 源数据库当前数据集合（通常来自数据库A）
//	b - 目标数据库最新数据集合（通常来自数据库B）
//
// 返回值：
//
//	error - 操作过程中发生的错误，包含上下文信息
//
// 执行流程：
// 1. 开启数据库事务
// 2. 批量删除源数据库中存在但目标库没有的记录
// 3. 批量插入/更新目标数据库的最新数据
// 4. 提交事务（任意步骤失败则自动回滚）
func atomicUpdate(db *sql.DB, a, b []Channel) error {
	// 开启数据库事务（保证原子性操作）
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("事务启动失败: %w", err)
	}
	// 异常恢复机制，确保事务最终状态
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback() // 回滚未提交的事务
			panic(p)      // 重新抛出panic保持原有行为
		}
	}()

	// 步骤1：清理冗余数据
	// 当源数据库有数据时，比对出需要删除的ID列表
	if len(a) > 0 {
		deleteIDs := getDeleteIDs(a, b)
		if len(deleteIDs) > 0 {
			// 执行批量删除操作
			if err := batchDelete(tx, deleteIDs); err != nil {
				return err // 返回包含上下文链的错误
			}
		}
	}

	// 步骤2：同步最新数据
	// 使用UPSERT操作（存在则更新，不存在则插入）
	if err := batchUpsert(tx, b); err != nil {
		return err
	}

	// 提交事务（只有所有操作成功才会执行）
	return tx.Commit()
}

// getDeleteIDs 识别需要删除的冗余ID列表（B中不存在的A记录）
// 参数：
//
//	a - 源数据库数据集（通常来自数据库A）
//	b - 目标数据库数据集（通常来自数据库B）
//
// 返回值：
//
//	[]interface{} - 需要删除的ID集合（适配SQL的IN查询参数格式）
func getDeleteIDs(a, b []Channel) []interface{} {
	// 创建目标数据集哈希表用于快速查找
	bMap := make(map[int]bool)
	for _, ch := range b {
		bMap[ch.Id] = true // 标记目标库存在的ID
	}

	var deleteIDs []interface{}
	// 遍历源数据找出目标库不存在的记录
	for _, ch := range a {
		if !bMap[ch.Id] {
			deleteIDs = append(deleteIDs, ch.Id) // 收集需要删除的ID
		}
	}
	return deleteIDs
}

// 批量删除操作
func batchDelete(tx *sql.Tx, ids []interface{}) error {
	query := "DELETE FROM channels WHERE id IN (?" + strings.Repeat(",?", len(ids)-1) + ")"
	_, err := tx.Exec(query, ids...)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("批量删除失败: %w", err)
	}
	return nil
}

// 批量插入/更新操作
func batchUpsert(tx *sql.Tx, channels []Channel) error {
	stmt, err := tx.Prepare("INSERT INTO channels (id, name) VALUES (?,?) ON DUPLICATE KEY UPDATE name=VALUES(name)")
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("预处理失败: %w", err)
	}
	defer stmt.Close()

	for _, ch := range channels {
		if _, err := stmt.Exec(ch.Id, ch.Name); err != nil {
			tx.Rollback()
			return fmt.Errorf("插入失败(ID:%d): %w", ch.Id, err)
		}
	}
	return nil
}

// 获取channels数据（复用已有数据库连接）
func getChannels(db *sql.DB) ([]Channel, error) {
	strFields := fmt.Sprintf("%s,%s", "id", "name")
	strQuery := fmt.Sprintf("SELECT %s FROM channels", strFields)
	rows, err := db.Query(strQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var ch Channel
		if err := rows.Scan(&ch.Id, &ch.Name); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// initDBConnection 创建并配置数据库连接池
// 参数：
//
//	dsn - 数据库连接字符串，格式：用户名:密码@tcp(地址:端口)/数据库名
//
// 返回值：
//
//	*sql.DB - 配置好的数据库连接对象
//
// 执行流程：
// 1. 创建初始数据库连接
// 2. 执行心跳检测验证连接有效性
// 3. 配置连接池参数（最大连接数和连接生命周期）
func initDBConnection(dsn string) *sql.DB {
	// 创建数据库连接对象（使用MySQL驱动）
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err) // 致命错误直接终止程序
	}

	// 执行Ping测试连接可用性
	if err := db.Ping(); err != nil {
		log.Fatalf("数据库心跳检测失败: %v", err) // 连接不可用则终止程序
	}

	// 配置连接池参数
	db.SetMaxOpenConns(20)                 // 最大并发连接数
	db.SetConnMaxLifetime(5 * time.Minute) // 连接最大存活时间

	return db
}
