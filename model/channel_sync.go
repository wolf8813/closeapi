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

// type Channel struct {
// 	ID        int       `gorm:"primaryKey;column:id"`
// 	Name      string    `gorm:"type:varchar(100);uniqueIndex;column:name"`
// 	Status    int       `gorm:"default:1;index"`
// 	Weight    int       `gorm:"default:0"`
// 	CreatedAt time.Time `gorm:"autoCreateTime"`
// 	UpdatedAt time.Time `gorm:"autoUpdateTime"`
// }

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

// StartChannelSync 使用GORM重构定时同步服务
func StartChannelSync() {
	dbA := initGORMConnection(getMySQLDSN("A"))
	dbB := initGORMConnection(getMySQLDSN("B"))

	sqlDB, _ := dbA.DB()
	defer sqlDB.Close()
	sqlDB, _ = dbB.DB()
	defer sqlDB.Close()

	for {
		now := time.Now()
		next := now.Truncate(1 * time.Minute).Add(1 * time.Minute) // 修改为1分钟间隔
		waitDuration := next.Sub(now)
		common.SysLog(fmt.Sprintf("[ChannelSync] 当前时间: %s, 下次同步时间: %s, 等待时间: %s",
			now.Format("2006-01-02 15:04:05"), next.Format("2006-01-02 15:04:05"), waitDuration))

		time.Sleep(waitDuration)

		syncChannels(dbA, dbB)
	}
}

// syncChannels 核心同步逻辑
func syncChannels(dbA, dbB *gorm.DB) {
	startTime := time.Now()
	common.SysLog(fmt.Sprintf("[ChannelSync] 开始同步 channels 表 (%s)", startTime.Format("2006-01-02 15:04:05")))

	var allChannelsA, allChannelsB []Channel

	// 分页加载数据库A（每批500条）
	if err := dbA.Where("id>0").FindInBatches(&allChannelsA, 500, func(tx *gorm.DB, batch int) error {
		return nil
	}).Error; err != nil {
		common.SysError(fmt.Sprintf("获取MySQL-A数据失败: %v", err))
		return
	}

	// 分页加载数据库B（每批500条）
	if err := dbB.Where("id>0").FindInBatches(&allChannelsB, 500, func(tx *gorm.DB, batch int) error {
		return nil
	}).Error; err != nil {
		common.SysError(fmt.Sprintf("获取MySQL-B数据失败: %v", err))
		return
	}

	if err := atomicGORMUpdate(dbA, allChannelsA, allChannelsB); err != nil {
		common.SysError(fmt.Sprintf("同步失败: %v", err))
	} else {
		common.SysLog(fmt.Sprintf("[ChannelSync] 同步完成，耗时 %v", time.Since(startTime).Round(time.Millisecond)))
	}
}

// atomicGORMUpdate 原子事务更新
func atomicGORMUpdate(db *gorm.DB, a, b []Channel) error {
	return db.Transaction(func(tx *gorm.DB) (err error) {
		// 添加 defer 统一处理错误日志
		defer func() {
			if err != nil {
				common.SysError(fmt.Sprintf("数据库事务操作失败: %v", err))
			}
		}()

		if len(a) > 0 {
			deleteIDs := getDeleteIDs(a, b)
			//日志记录，删除了哪些ids
			common.SysLog(fmt.Sprintf("[ChannelSync] 删除了以下ids: %v", deleteIDs))
			if len(deleteIDs) > 0 {
				if err = tx.Where("id IN ?", deleteIDs).Delete(&Channel{}).Error; err != nil {
					return fmt.Errorf("删除冗余记录失败: %w", err) // 包装原始错误
				}
			}
		}

		// 执行批量插入操作（冲突时跳过）
		if err = tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "name"}},
			DoNothing: true,
		}).CreateInBatches(&b, 200).Error; err != nil {
			return fmt.Errorf("批量插入失败: %w", err) // 包装原始错误
		}
		return nil
	})
}

// getDeleteIDs 识别需要删除的冗余ID列表（基于name不存在于B的A记录）
// 参数：
//
//	a - 源数据库数据集（通常来自数据库A）
//	b - 目标数据库数据集（通常来自数据库B）
//
// 返回值：
//
//	[]interface{} - 需要删除的ID集合（适配SQL的IN查询参数格式）
func getDeleteIDs(a, b []Channel) []int {
	// 创建目标数据集哈希表用于快速查找（基于name字段）
	bMap := make(map[string]bool) // 修改为string类型作为key
	for _, ch := range b {
		bMap[ch.Name] = true // 使用name作为唯一标识
	}

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
//
//	dsn : 数据库连接字符串，格式示例："user:password@tcp(host:port)/dbname"
//
// 返回值：
//
//	*gorm.DB : 初始化完成的GORM数据库实例
//
// 注意：
//  1. 连接池配置参数：
//     - 最大打开连接数：20
//     - 最大空闲连接数：10
//     - 连接最大空闲时间：30分钟
//     - 连接最大存活时间：5分钟
//  2. 数据库连接失败会直接触发log.Fatal
func initGORMConnection(dsn string) *gorm.DB {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		//Logger: logger.Default.LogMode(logger.Info), // 开启 SQL 日志记录
	})
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}

	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)                  // 新增空闲连接数
	sqlDB.SetConnMaxIdleTime(30 * time.Minute) // 新增空闲时间
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	return db
}
