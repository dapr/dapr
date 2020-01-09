// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package sqlserver

import (
	"database/sql"
	"fmt"
)

type migrator interface {
	executeMigrations() (migrationResult, error)
}

type migration struct {
	store *SQLServer
}

type migrationResult struct {
	bulkDeleteProcName       string
	bulkDeleteProcFullName   string
	itemRefTableTypeName     string
	upsertProcName           string
	upsertProcFullName       string
	pkColumnType             string
	getCommand               string
	deleteWithETagCommand    string
	deleteWithoutETagCommand string
}

func newMigration(store *SQLServer) migrator {
	return &migration{
		store: store,
	}
}

/* #nosec */
func (m *migration) executeMigrations() (migrationResult, error) {
	r := migrationResult{
		bulkDeleteProcName:       fmt.Sprintf("sp_BulkDelete_%s", m.store.tableName),
		itemRefTableTypeName:     fmt.Sprintf("[%s].%s_Table", m.store.schema, m.store.tableName),
		upsertProcName:           fmt.Sprintf("sp_Upsert_%s", m.store.tableName),
		getCommand:               fmt.Sprintf("SELECT [Data], [RowVersion] FROM [%s].[%s] WHERE [Key] = @Key", m.store.schema, m.store.tableName),
		deleteWithETagCommand:    fmt.Sprintf(`DELETE [%s].[%s] WHERE [Key]=@Key AND [RowVersion]=@RowVersion`, m.store.schema, m.store.tableName),
		deleteWithoutETagCommand: fmt.Sprintf(`DELETE [%s].[%s] WHERE [Key]=@Key`, m.store.schema, m.store.tableName),
	}

	r.bulkDeleteProcFullName = fmt.Sprintf("[%s].%s", m.store.schema, r.bulkDeleteProcName)
	r.upsertProcFullName = fmt.Sprintf("[%s].%s", m.store.schema, r.upsertProcName)

	switch m.store.keyType {
	case StringKeyType:
		r.pkColumnType = fmt.Sprintf("NVARCHAR(%d)", m.store.keyLength)

	case UUIDKeyType:
		r.pkColumnType = "uniqueidentifier"

	case IntegerKeyType:
		r.pkColumnType = "int"
	}

	db, err := sql.Open("sqlserver", m.store.connectionString)
	if err != nil {
		return r, err
	}

	defer db.Close()

	err = m.ensureSchemaExists(db)
	if err != nil {
		return r, fmt.Errorf("failed to create db schema: %v", err)
	}

	err = m.ensureTableExists(db, r)
	if err != nil {
		return r, fmt.Errorf("failed to create db table: %v", err)
	}

	err = m.ensureStoredProcedureExists(db, r)
	if err != nil {
		return r, fmt.Errorf("failed to create stored procedures: %v", err)
	}

	for _, ix := range m.store.indexedProperties {
		err = m.ensureIndexedPropertyExists(ix, db)
		if err != nil {
			return r, err
		}
	}

	return r, nil
}

func runCommand(tsql string, db *sql.DB) error {
	_, err := db.Exec(tsql)
	if err != nil {
		return err
	}

	return nil
}

/* #nosec */
func (m *migration) ensureIndexedPropertyExists(ix IndexedProperty, db *sql.DB) error {
	indexName := "IX_" + ix.ColumnName

	tsql := fmt.Sprintf(`
	IF (NOT EXISTS(SELECT object_id
				   FROM sys.indexes 
				   WHERE object_id = OBJECT_ID('[%s].%s')
    					AND name='%s'))
		CREATE INDEX %s ON [%s].[%s]([%s])`,
		m.store.schema,
		m.store.tableName,
		indexName,
		indexName,
		m.store.schema,
		m.store.tableName,
		ix.ColumnName)

	return runCommand(tsql, db)
}

/* #nosec */
func (m *migration) ensureSchemaExists(db *sql.DB) error {
	tsql := fmt.Sprintf(`
	IF NOT EXISTS(SELECT * FROM sys.schemas WHERE name = N'%s')
		EXEC('CREATE SCHEMA [%s]')`,
		m.store.schema, m.store.schema)

	return runCommand(tsql, db)
}

/* #nosec */
func (m *migration) ensureTableExists(db *sql.DB, r migrationResult) error {
	tsql := fmt.Sprintf(`
	IF NOT EXISTS (SELECT * FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s')
    	CREATE TABLE [%s].[%s] (
			[Key] 			%s CONSTRAINT PK_%s PRIMARY KEY,
			[Data]			NVARCHAR(MAX) NOT NULL,
			[InsertDate] 	DateTime2 NOT NULL DEFAULT(GETDATE()),
			[UpdateDate] 	DateTime2 NULL,`,
		m.store.schema, m.store.tableName, m.store.schema, m.store.tableName, r.pkColumnType, m.store.tableName)

	if m.store.indexedProperties != nil {
		for _, prop := range m.store.indexedProperties {
			if prop.Type != "" {
				tsql += fmt.Sprintf("\n		[%s] AS CONVERT(%s, JSON_VALUE(Data, '$.%s')) PERSISTED,", prop.ColumnName, prop.Type, prop.Property)
			} else {
				tsql += fmt.Sprintf("\n		[%s] AS JSON_VALUE(Data, '$.%s') PERSISTED,", prop.ColumnName, prop.Property)
			}
		}
	}

	tsql += `
		[RowVersion] 	ROWVERSION NOT NULL)
	`
	return runCommand(tsql, db)
}

/* #nosec */
func (m *migration) ensureTypeExists(db *sql.DB, mr migrationResult) error {
	tsql := fmt.Sprintf(`
	IF type_id('[%s].%s_Table') IS NULL
		CREATE TYPE [%s].%s_Table AS TABLE
		(
			[Key]           		%s NOT NULL,
			[RowVersion]			BINARY(8)
		)
	`, m.store.schema, m.store.tableName, m.store.schema, m.store.tableName, mr.pkColumnType)

	return runCommand(tsql, db)
}

/* #nosec */
func (m *migration) ensureBulkDeleteStoredProcedureExists(db *sql.DB, mr migrationResult) error {
	tsql := fmt.Sprintf(`
		CREATE PROCEDURE %s 
			@itemsToDelete %s READONLY
		AS 
			DELETE [%s].[%s]
			FROM [%s].[%s] x
			JOIN @itemsToDelete i ON i.[Key] = x.[Key] AND (i.[RowVersion] IS NULL OR i.[RowVersion] = x.[RowVersion])`,
		mr.bulkDeleteProcFullName,
		mr.itemRefTableTypeName,
		m.store.schema,
		m.store.tableName,
		m.store.schema,
		m.store.tableName)

	return m.createStoredProcedureIfNotExists(db, mr.bulkDeleteProcName, tsql)
}

func (m *migration) ensureStoredProcedureExists(db *sql.DB, mr migrationResult) error {
	err := m.ensureTypeExists(db, mr)
	if err != nil {
		return err
	}

	err = m.ensureBulkDeleteStoredProcedureExists(db, mr)
	if err != nil {
		return err
	}

	err = m.ensureUpsertStoredProcedureExists(db, mr)
	if err != nil {
		return err
	}

	return nil
}

/* #nosec */
func (m *migration) createStoredProcedureIfNotExists(db *sql.DB, name string, escapedDefinition string) error {
	tsql := fmt.Sprintf(`
	IF NOT EXISTS (SELECT * FROM sys.objects WHERE object_id = OBJECT_ID(N'[%s].[%s]') AND type in (N'P', N'PC'))
	BEGIN
		execute ('%s')
	END`,
		m.store.schema,
		name,
		escapedDefinition)

	return runCommand(tsql, db)
}

/* #nosec */
func (m *migration) ensureUpsertStoredProcedureExists(db *sql.DB, mr migrationResult) error {
	tsql := fmt.Sprintf(`
		CREATE PROCEDURE %s (
			@Key 			%s,
			@Data 			NVARCHAR(MAX),
			@RowVersion 	BINARY(8))
		AS
			IF (@RowVersion IS NOT NULL)
			BEGIN
				UPDATE [%s]
				SET [Data]=@Data, UpdateDate=GETDATE()
				WHERE [Key]=@Key AND RowVersion = @RowVersion

				RETURN
			END
			
			BEGIN TRY
				INSERT INTO [%s] ([Key], [Data]) VALUES (@Key, @Data);
			END TRY

			BEGIN CATCH
				IF ERROR_NUMBER() IN (2601, 2627) 
				UPDATE [%s]
				SET [Data]=@Data, UpdateDate=GETDATE()
				WHERE [Key]=@Key AND RowVersion = ISNULL(@RowVersion, RowVersion)
			END CATCH`,
		mr.upsertProcFullName,
		mr.pkColumnType,
		m.store.tableName,
		m.store.tableName,
		m.store.tableName)

	return m.createStoredProcedureIfNotExists(db, mr.upsertProcName, tsql)
}
