package pdnsmodel

import "database/sql"

type Domain struct {
	ID             uint           `gorm:"primary_key"`
	Name           string         `gorm:"type:varchar(255);not null"`
	Master         sql.NullString `gorm:"type:varchar(128)"`
	LastCheck      sql.NullInt64
	Type           string `gorm:"type:varchar(6);not null"`
	NotifiedSerial sql.NullInt64
	Account        sql.NullString `gorm:"type:varchar(40)"`
}

type Record struct {
	ID         uint `gorm:"primary_key"`
	DomainId   uint
	Name       string `gorm:"type:varchar(255);not null"`
	Type       string `gorm:"type:varchar(10)"`
	Content    string `gorm:"type:text"`
	Ttl        uint32
	Prio       int
	ChangeDate int
	Disabled   bool
	//ordername             VARCHAR(255) BINARY DEFAULT NULL,
	//auth                  TINYINT(1) DEFAULT 1,
}
