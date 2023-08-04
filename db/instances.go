package db

import (
	"errors"
	"time"

	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/zebrunner/esg/config"
)

type Instance struct {
	Type      string    `db:"type"`
	Cpu       int64     `db:"cpu"`
	Memory    int64     `db:"memory"`
	UpdatedAt time.Time `db:"updated_at"`
}

func CreateInstance(instance *Instance) error {
	instanceDuplicate, _ := GetInstance(instance.Type)
	if instanceDuplicate != nil {
		return errors.New("instance with this type already exists")
	}

	createQuery := `INSERT INTO instances (type, cpu, memory, updated_at) VALUES ($1, $2, $3, $4)`
	_, err := config.DbConnection.Exec(createQuery, instance.Type, instance.Cpu, instance.Memory, time.Now())
	if err != nil {
		return err
	}

	return nil
}

func GetInstance(instanceType string) (*Instance, error) {
	getQuery := `SELECT type, cpu, memory, updated_at FROM instances WHERE type = $1`
	instance := &Instance{}
	err := config.DbConnection.Get(instance, getQuery, instanceType)
	if err != nil {
		return nil, err
	}

	return instance, nil
}

func RefreshInstance(instance *Instance) error {
	updateQuery := `UPDATE instances
		SET cpu = $1,
		memory = $2,
		updated_at = $3
		WHERE type = $4
	`
	_, err := config.DbConnection.Exec(updateQuery, instance.Cpu, instance.Memory, time.Now(), instance.Type)
	if err != nil {
		return err
	}

	return nil
}
