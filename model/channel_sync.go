// Package model 提供数据库模型和同步功能
// 本文件实现了MySQL数据库A和B之间channels表的定时同步功能
// 主要特性：
// 1. 支持跨数据库的channels表数据同步
// 2. 使用GORM进行数据库操作，提供更好的类型安全
// 3. 采用原子事务保证数据一致性
// 4. 支持增量同步，避免全表操作
// 5. 定时执行，默认每1分钟同步一次
package model

import (
	"fmt"
	"log"
	"time"

	"one-api/common"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// getMySQLDSN 根据数据库类型生成MySQL连接字符串
// 参数 dbType: 数据库标识（A/B）
// 返回值: MySQL DSN连接字符串
// 支持的数据库类型：
// - "A": 本地数据库 (127.0.0.1:3306)
// - "B": 远程数据库 (38.147.104.170:3366)
func getMySQLDSN(dbType string) string {
	switch dbType {
	case "A":
		// 数据库A连接配置（本地数据库）
		return fmt.Sprintf("%s:%s@tcp(%s)/%s",
			"root",
			"new.1234",
			"127.0.0.1:3306",
			"mysql")
	case "B":
		// 数据库B连接配置（远程数据库）
		return fmt.Sprintf("%s:%s@tcp(%s)/%s",
			"root",
			"yeqiu669.",
			"38.147.104.170:3366",
			"new-api")
	default:
		return "" // 未知数据库类型返回空字符串
	}
}

// StartChannelSync 启动定时同步服务的主函数
// 功能说明：
// 1. 初始化两个数据库连接（A和B）
// 2. 设置定时器，每1分钟执行一次同步
// 3. 记录同步日志，包括当前时间、下次同步时间和等待时间
// 4. 持续运行，直到程序退出
func StartChannelSync() {
	// 初始化数据库连接
	dbA := initGORMConnection(getMySQLDSN("A")) // 连接数据库A
	dbB := initGORMConnection(getMySQLDSN("B")) // 连接数据库B

	// 获取底层sql.DB对象并设置延迟关闭
	sqlDB, _ := dbA.DB()
	defer sqlDB.Close()
	sqlDB, _ = dbB.DB()
	defer sqlDB.Close()

	// 无限循环，执行定时同步
	for {
		// 计算下次同步时间（每分钟的整点）
		now := time.Now()
		next := now.Truncate(1 * time.Minute).Add(1 * time.Minute) // 修改为1分钟间隔
		waitDuration := next.Sub(now)

		// 记录同步计划日志
		common.SysLog(fmt.Sprintf("[ChannelSync] 当前时间: %s, 下次同步时间: %s, 等待时间: %s",
			now.Format("2006-01-02 15:04:05"), next.Format("2006-01-02 15:04:05"), waitDuration))

		// 等待到下次同步时间
		time.Sleep(waitDuration)

		// 执行同步操作
		syncChannels(dbA, dbB)
	}
}

// syncChannels 执行channels表同步的核心逻辑
// 参数：
//   - dbA: 数据库A的GORM连接
//   - dbB: 数据库B的GORM连接
//
// 同步流程：
// 1. 记录同步开始时间
// 2. 分页加载两个数据库的channels数据
// 3. 执行原子更新操作
// 4. 记录同步完成时间和耗时
func syncChannels(dbA, dbB *gorm.DB) {
	// 记录同步开始时间
	startTime := time.Now()
	common.SysLog(fmt.Sprintf("[ChannelSync] 开始同步 channels 表 (%s)", startTime.Format("2006-01-02 15:04:05")))

	// 声明变量存储两个数据库的channels数据
	var allChannelsA, allChannelsB []Channel

	// 分页加载数据库A的channels数据（每批500条，避免内存溢出）
	if err := dbA.Where("id>0").FindInBatches(&allChannelsA, 500, func(tx *gorm.DB, batch int) error {
		return nil // 空回调函数，仅用于分页加载
	}).Error; err != nil {
		common.SysError(fmt.Sprintf("获取MySQL-A数据失败: %v", err))
		return
	}

	// 分页加载数据库B的channels数据（每批500条）
	if err := dbB.Where("id>0").FindInBatches(&allChannelsB, 500, func(tx *gorm.DB, batch int) error {
		return nil // 空回调函数，仅用于分页加载
	}).Error; err != nil {
		common.SysError(fmt.Sprintf("获取MySQL-B数据失败: %v", err))
		return
	}

	// 执行原子更新操作
	if err := atomicGORMUpdate(dbA, allChannelsA, allChannelsB); err != nil {
		common.SysError(fmt.Sprintf("同步失败: %v", err))
	} else {
		// 记录同步成功日志，包含耗时信息
		common.SysLog(fmt.Sprintf("[ChannelSync] 同步完成，耗时 %v", time.Since(startTime).Round(time.Millisecond)))
	}
}

// atomicGORMUpdate 使用GORM事务执行原子更新操作
// 参数：
//   - db: 目标数据库连接（通常是数据库A）
//   - a: 源数据库数据集（数据库A的当前数据）
//   - b: 目标数据集（数据库B的数据，将同步到A）
//
// 返回值：
//   - error: 操作结果，成功返回nil，失败返回错误信息
//
// 更新策略：
// 1. 删除在A中存在但在B中不存在的记录（基于name字段）
// 2. 插入B中的新记录（冲突时跳过，避免重复插入）
func atomicGORMUpdate(db *gorm.DB, a, b []Channel) error {
	// 使用GORM事务确保操作的原子性
	return db.Transaction(func(tx *gorm.DB) (err error) {
		// 添加 defer 统一处理错误日志
		defer func() {
			if err != nil {
				common.SysError(fmt.Sprintf("数据库事务操作失败: %v", err))
			}
		}()

		// 如果源数据库有数据，执行删除操作
		if len(a) > 0 {
			// 获取需要删除的ID列表（在A中存在但在B中不存在的记录）
			deleteIDs := getDeleteIDs(a, b)

			// 记录删除操作的日志
			common.SysLog(fmt.Sprintf("[ChannelSync] 删除了以下ids: %v", deleteIDs))

			// 批量删除冗余记录
			if len(deleteIDs) > 0 {
				if err = tx.Where("id IN ?", deleteIDs).Delete(&Channel{}).Error; err != nil {
					return fmt.Errorf("删除冗余记录失败: %w", err) // 包装原始错误
				}
			}
		}

		// 执行批量插入操作（冲突时跳过）
		// 使用ON CONFLICT DO NOTHING策略，避免重复插入
		if err = tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "name"}}, // 基于name字段判断冲突
			DoNothing: true,                            // 冲突时不做任何操作
		}).CreateInBatches(&b, 200).Error; err != nil { // 每批插入200条记录
			return fmt.Errorf("批量插入失败: %w", err) // 包装原始错误
		}
		return nil
	})
}

// getDeleteIDs 识别需要删除的冗余ID列表（基于name不存在于B的A记录）
// 参数：
//   - a: 源数据库数据集（通常来自数据库A）
//   - b: 目标数据库数据集（通常来自数据库B）
//
// 返回值：
//   - []int: 需要删除的ID集合（适配SQL的IN查询参数格式）
//
// 算法说明：
// 1. 创建B数据集的哈希表，用于快速查找
// 2. 遍历A数据集，找出在B中不存在的记录
// 3. 返回需要删除的ID列表
func getDeleteIDs(a, b []Channel) []int {
	// 创建目标数据集哈希表用于快速查找（基于name字段）
	bMap := make(map[string]bool) // 修改为string类型作为key
	for _, ch := range b {
		bMap[ch.Name] = true // 使用name作为唯一标识
	}

	// 收集需要删除的ID
	var deleteIDs []int
	// 遍历源数据找出目标库不存在的记录（基于name判断）
	for _, ch := range a {
		if !bMap[ch.Name] { // 比较name字段
			deleteIDs = append(deleteIDs, ch.Id) // 仍然收集需要删除的ID
		}
	}
	return deleteIDs
}

// initGORMConnection 初始化GORM数据库连接池
// 参数：
//   - dsn: 数据库连接字符串，格式示例："user:password@tcp(host:port)/dbname"
//
// 返回值：
//   - *gorm.DB: 初始化完成的GORM数据库实例
//
// 连接池配置说明：
// 1. 最大打开连接数：20（同时支持的最大数据库连接数）
// 2. 最大空闲连接数：10（连接池中保持的空闲连接数）
// 3. 连接最大空闲时间：30分钟（空闲连接超过此时间将被关闭）
// 4. 连接最大存活时间：5分钟（连接超过此时间将被关闭）
//
// 注意：
// - 数据库连接失败会直接触发log.Fatal，导致程序退出
// - 建议在生产环境中添加重试机制
func initGORMConnection(dsn string) *gorm.DB {
	// 使用GORM打开数据库连接
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		//Logger: logger.Default.LogMode(logger.Info), // 开启 SQL 日志记录（调试时使用）
	})
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}

	// 获取底层sql.DB对象以配置连接池
	sqlDB, _ := db.DB()

	// 配置连接池参数
	sqlDB.SetMaxOpenConns(20)                  // 设置最大打开连接数
	sqlDB.SetMaxIdleConns(10)                  // 设置最大空闲连接数
	sqlDB.SetConnMaxIdleTime(30 * time.Minute) // 设置连接最大空闲时间
	sqlDB.SetConnMaxLifetime(5 * time.Minute)  // 设置连接最大存活时间

	return db
}
