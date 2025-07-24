package controller

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"one-api/common"
)

type Channel struct {
	ID   int
	Name string
}

// getMySQLDSN 根据数据库类型生成MySQL连接字符串
// 参数 dbType: 数据库标识（A/B）
// 返回值: MySQL DSN连接字符串
func getMySQLDSN(dbType string) string {
	switch dbType {
	case "A":
		return fmt.Sprintf("%s:%s@tcp(%s)/%s",
			os.Getenv("root"),
			os.Getenv("new.1234"),
			os.Getenv("127.0.0.1:3306"),
			os.Getenv("mysql"))
	case "B":
		return fmt.Sprintf("%s:%s@tcp(%s)/%s",
			os.Getenv("root"),
			os.Getenv("yeqiu669."),
			os.Getenv("38.147.104.170:3366"),
			os.Getenv("new-api"))
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

func syncChannels(dbA, dbB *sql.DB) {
	startTime := time.Now()
	common.SysLog(fmt.Sprintf("[ChannelSync] 开始同步 channels 表 (%s)", startTime.Format("2006-01-02 15:04:05")))

	// 获取双数据源数据
	channelsA, err := getChannels(dbA)
	if err != nil {
		common.LogError(fmt.Sprintf("获取MySQL-A数据失败: %v", err))
		return
	}

	channelsB, err := getChannels(dbB)
	if err != nil {
		common.LogError(fmt.Sprintf("获取MySQL-B数据失败: %v", err))
		return
	}

	// 执行原子更新
	if err := atomicUpdate(dbA, channelsA, channelsB); err != nil {
		common.LogError(fmt.Sprintf("同步失败: %v", err))
	} else {
		common.SysLog(fmt.Sprintf("[ChannelSync] 同步完成，耗时 %v", time.Since(startTime).Round(time.Millisecond)))
	}
}

func atomicUpdate(db *sql.DB, a, b []Channel) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("事务启动失败: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()

	// 批量删除A中存在但B中没有的记录
	if len(a) > 0 {
		deleteIDs := getDeleteIDs(a, b)
		if len(deleteIDs) > 0 {
			if err := batchDelete(tx, deleteIDs); err != nil {
				return err
			}
		}
	}

	// 批量插入/更新B的记录
	if err := batchUpsert(tx, b); err != nil {
		return err
	}

	return tx.Commit()
}

// 获取需要删除的ID列表（B中不存在的A记录）
func getDeleteIDs(a, b []Channel) []interface{} {
	bMap := make(map[int]bool)
	for _, ch := range b {
		bMap[ch.ID] = true
	}

	var deleteIDs []interface{}
	for _, ch := range a {
		if !bMap[ch.ID] {
			deleteIDs = append(deleteIDs, ch.ID)
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
		if _, err := stmt.Exec(ch.ID, ch.Name); err != nil {
			tx.Rollback()
			return fmt.Errorf("插入失败(ID:%d): %w", ch.ID, err)
		}
	}
	return nil
}

// 获取channels数据（复用已有数据库连接）
func getChannels(db *sql.DB) ([]Channel, error) {
	rows, err := db.Query("SELECT id, name FROM channels")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var ch Channel
		if err := rows.Scan(&ch.ID, &ch.Name); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// 数据库连接通用方法
func initDBConnection(dsn string) *sql.DB {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	if err := db.Ping(); err != nil {
		log.Fatalf("数据库心跳检测失败: %v", err)
	}
	db.SetMaxOpenConns(20)
	db.SetConnMaxLifetime(5 * time.Minute)
	return db
}
