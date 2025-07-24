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
	"testing"
)

func Test_syncChannels(t *testing.T) {
	// 初始化主数据库连接（数据库A）
	dbA := initDBConnection(getMySQLDSN("A"))
	// 初始化备用数据库连接（数据库B）
	dbB := initDBConnection(getMySQLDSN("B"))
	defer dbA.Close() // 确保程序退出时释放数据库连接
	defer dbB.Close() // 确保程序退出时释放数据库连接

	syncChannels(dbA, dbB)
}

func Test_getChannels(t *testing.T) {
	// 初始化主数据库连接（数据库A）
	dbA := initDBConnection(getMySQLDSN("A"))
	defer dbA.Close() // 确保程序退出时释放数据库连接

	channelsA, err := getChannels(dbA)
	if err != nil {
		t.Errorf("获取MySQL-A数据失败: %v", err)
	} else {
		t.Logf("MySQL-A数据: %v", channelsA)
	}

}
