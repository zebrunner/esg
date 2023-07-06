package db

import (
	// "database/sql"

	// "github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"

	"github.com/jackc/pgtype"
	// pgx "github.com/jackc/pgx/v4"
	_ "github.com/jackc/pgx/v4/stdlib"
)

type TaskDefinition struct {
	Family                   string           `db:"task_family"`
	Schema                   string           `db:"schema"`
	RegisteredDefinitionHash string           `db:"registered_task_hash"`
	Tag                      int64            `db:"tag"`
	UpdatedAt                pgtype.Timestamp `db:"updated_at"`
	OverriddenDefinitionHash string           `db:"overridden_task_hash"`
}

func CreateDefinition(td *TaskDefinition) (string, *utils.APIError) {
	// 	BEGIN TRANSACTION
	//     DECLARE @famiylyId INTEGER
	//     if (SELECT count(family_id) FROM families f WHERE f.task_family = 'linux-chrome-latest') = 0
	//         INSERT INTO families (task_family) VALUES ('linux-chrome-latest')
	//         SET @famiylyId = LAST_INSERT_ID()
	//     ELSE
	//         SET @famiylyId = SELECT family_id FROM families WHERE families.task_family = 'linux-chrome-latest'

	//     DECLARE @schemaId INTEGER
	//     if (SELECT count(schema_id) FROM schemas s WHERE s.schema = 'mitm-executor-uploader-recorder') = 0
	//         INSERT INTO families (task_family) VALUES ('mitm-executor-uploader-recorder')
	//         SET @schemaId = LAST_INSERT_ID()
	//     ELSE
	//         SET @schemaId = SELECT schema_id FROM schemas WHERE schemas.schema = 'mitm-executor-uploader-recorder'

	//     if (SELECT count(*) FROM familiesSchemas fs WHERE fs.family_id = @famiylyId AND fs.schema_id = @schemaId) = 0
	//         INSERT INTO familiesSchemas (family_id, schema_id) VALUES (@famiylyId, @schemaId)

	//     INSERT INTO definitions (register_hash, tag, updated_at, full_hash, schema_id) VALUES ("hash1", "latest", "time", "hash2", @schemaId)
	// COMMIT
	return "", nil
}

func GetDefinition(family string, schema string) (*TaskDefinition, error) {
	// SELECT
	//     d.register_hash, d.override_hash
	// FROM families f
	// INNER JOIN familiesSchemas fs ON f.family_id = fs.family_id
	// INNER JOIN schemas s ON fs.schema_id = s.schema_id
	// INNER JOIN definitions d ON s.schema_id = d.schema_id;
	// WHERE f.task_family = "" AND s.schema = ""

	// if err != nil {
	// if err == pgx.ErrNoRows || err == sql.ErrNoRows {
	// return "", nil
	// }
	// return "", err
	// }
	return nil, nil
}

func RefreshTag(td *TaskDefinition) (string, error) {
	return "", nil
}
