package model

import (
	"testing"
)

func Test_syncChannels(t *testing.T) {
	dbA := initGORMConnection(getMySQLDSN("A"))
	dbB := initGORMConnection(getMySQLDSN("B"))

	sqlDB, _ := dbA.DB()
	defer sqlDB.Close()
	sqlDB, _ = dbB.DB()
	defer sqlDB.Close()

	syncChannels(dbA, dbB)
}
