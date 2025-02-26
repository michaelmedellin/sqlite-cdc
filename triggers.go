// SPDX-FileCopyrightText: © 2024 Kevin Conway <kevin@conway0.com>
// SPDX-License-Identifier: Apache-2.0

package cdc

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
	"log"
)

const (
	defaultLogTableName = "__cdc_log"
	defaultMaxBatchSize = 50
)

type Option func(*TriggerEngine) error

// WithLogTableName specifies the name of the log table. This defaults to
// __cdc_log but may be customized if needed.
func WithLogTableName(name string) Option {
	return func(t *TriggerEngine) error {
		t.logTableName = name
		return nil
	}
}

// WithMaxBatchSize specifies the maximum number of changes to process in a
// single batch. This defaults to 50.
func WithMaxBatchSize(size int) Option {
	return func(t *TriggerEngine) error {
		t.maxBatchSize = size
		return nil
	}
}

// WithoutSubsecondTime can disable the use of subsecond timestamps in the log
// table. This is only needed for old versions of SQLite and should be avoided
// otherwise.
func WithoutSubsecondTime(v bool) Option {
	return func(t *TriggerEngine) error {
		t.subsec = !v
		return nil
	}
}

// WithBlobSupport can enable or disable the storage of BLOB columns in the log
// table. This defaults to false because of the performance impacts of encoding
// BLOB type data.
func WithBlobSupport(v bool) Option {
	return func(t *TriggerEngine) error {
		t.blobs = v
		return nil
	}
}

// WithSignal installs a custom awakening signal that triggers the inspection
// of the log table when in CDC mode. The default signal is a combination of
// a filesystem watcher that signals when the SQLite files have changed and a
// 250ms timer used as a backstop for any missed filesystem events.
func WithSignal(signal Signal) Option {
	return func(t *TriggerEngine) error {
		t.signal = signal
		return nil
	}
}

// NewTriggerEngine returns a CDC implementation based on table triggers.
//
// This implementation works with any SQLite driver and uses only SQL operations
// to implement CDC. For each specified table to monitor, the implementation
// creates triggers for AFTER INSERT, AFTER UPDATE, and AFTER DELETE. These
// triggers populate a log table, named __cdc_log by default. The log table
// entries contain effectively identical information as the Change struct.
//
// The before and after images are stored as JSON objects in the log table. The
// JSON objects are generated from the column names and values in the table.
//
// See the TriggerEngine documentation for more details.
func NewTriggerEngine(db *sql.DB, handler ChangesHandler, tables []*string, options ...Option) (CDC, error) {
    // Debug the database connection
    fmt.Println("DEBUG: Checking database connection...")
    if db == nil {
        fmt.Println("DEBUG: Database connection is nil!")
        return nil, fmt.Errorf("database connection is nil")
    }
    
    // Test the connection by pinging
    fmt.Println("DEBUG: Attempting to ping database...")
    if err := db.Ping(); err != nil {
        fmt.Printf("DEBUG: Database ping failed: %v\n", err)
        return nil, fmt.Errorf("database ping failed: %w", err)
    }
    fmt.Println("DEBUG: Database ping successful")
    
    // Check if tables are provided
    fmt.Printf("DEBUG: Monitoring %d tables\n", len(tables))
    for i, table := range tables {
        if table == nil {
            fmt.Printf("DEBUG: Table at index %d is nil\n", i)
        } else {
            fmt.Printf("DEBUG: Table[%d]: %s\n", i, *table)
        }
    }
    
    fmt.Println("DEBUG: Initializing database metadata...")
    meta, err := newDBMeta(db)
    if err != nil {
        fmt.Printf("DEBUG: Failed to initialize database metadata: %v\n", err)
        return nil, err
    }
    fmt.Printf("DEBUG: Database metadata initialized successfully: type=%T\n", meta)
    
    // Continue with initialization
    fmt.Println("DEBUG: Creating TriggerEngine instance...")
    result := &TriggerEngine{
        db:           db,
        meta:         meta,
        handler:      handler,
        tables:       tables,
        fnOnce:       &sync.Once{},
        closed:       make(chan any),
        closeOnce:    &sync.Once{},
        logTableName: defaultLogTableName,
        maxBatchSize: defaultMaxBatchSize,
        subsec:       true,
        blobs:        false,
    }
    
    // Apply options
    fmt.Printf("DEBUG: Applying %d options...\n", len(options))
    for i, opt := range options {
        fmt.Printf("DEBUG: Applying option %d\n", i)
        if err := opt(result); err != nil {
            fmt.Printf("DEBUG: Option %d failed: %v\n", i, err)
            return nil, err
        }
    }
    
    // Initialize signal if not provided
    if result.signal == nil {
        fmt.Println("DEBUG: No signal provided, creating default signals...")
        
        fmt.Println("DEBUG: Creating filesystem notification signal...")
        fsSignal, err := NewFSNotifySignal(db)
        if err != nil {
            fmt.Printf("DEBUG: Failed to create filesystem wake signal: %v\n", err)
            return nil, fmt.Errorf("failed to create filesystem wake signal: %w", err)
        }
        fmt.Println("DEBUG: Filesystem notification signal created successfully")
        
        fmt.Println("DEBUG: Creating time-based signal (250ms)...")
        timeSignal, err := NewTimeSignal(250 * time.Millisecond)
        if err != nil {
            fmt.Printf("DEBUG: Failed to create time wake signal: %v\n", err)
            return nil, fmt.Errorf("failed to create time wake signal: %w", err)
        }
        fmt.Println("DEBUG: Time-based signal created successfully")
        
        fmt.Println("DEBUG: Creating combined multi-signal...")
        signal, err := NewMultiSignal(fsSignal, timeSignal)
        if err != nil {
            fmt.Printf("DEBUG: Failed to create multi wake signal: %v\n", err)
            return nil, fmt.Errorf("failed to create multi wake signal: %w", err)
        }
        fmt.Println("DEBUG: Multi-signal created successfully")
        
        result.signal = signal
    } else {
        fmt.Println("DEBUG: Custom signal provided via options")
    }
    
    fmt.Println("DEBUG: TriggerEngine initialized successfully")
    return result, nil
}

// TriggerEngine implements CDC using table triggers.
//
// This implementation targets a specified set of tables and captures changes by
// using AFTER triggers to populate a change log table. The setup and teardown
// methods manage both the triggers and the log table. Currently, all target
// tables must be set up and torn down together and cannot be targeted
// individually.
//
// The bootstrap mode is implemented by selecting batches of records from target
// tables. These are passed through to the bound ChangesHandler as they are
// selected. Each table bootstrap begins with the specified BOOTSTRAP operation
// event. Because this implementation of CDC uses table triggers and a
// persistent chang log table, a bootstrap is usually only needed once after
// running setup. If your system encounters a critical fault and needs to
// rebuild state from a bootstrap then you can safely run bootstrap again.
// However, subsequent runs of bootstrap mode do not clear the change log table.
//
// The cdc mode is implemented by selecting batches of records from the change
// log table. The order of the log selection matches the natural sort order of
// the table which, itself, matches the order in which changes were made to the
// data. The frequency with which cdc mode checks for changes is determined by
// the bound Signal implementation. The default signal is a combination of a
// filesystem watcher and a time based interval. The filesystem watcher detects
// changes to the underlying SQLite files with the intent to handle changes as
// quickly as possible once they are persisted. However, the filesystem watcher
// is not infallible so a time based interval signal is included as a backstop.
// Generally, you are recommended to have some form of time based interval
// signal to augment any other signal choices.
//
// By default, all change log entries are recorded with a millisecond precision
// timestamp. This precision is only available in SQLite 3.42.0 and later. If
// any system accessing the SQLite database is older than 3.42.0 then you must
// disable the subsecond timestamp with the WithoutSubsecondTime option.
//
// By default, support for BLOB data is disabled and BLOB type columns are not
// included in change log records due to the performance impacts of encoding
// BLOB type data. If you need to handle BLOB type data then you must enable
// BLOB support with the WithBlobSupport option. Note, however, that this
// implementations identification of BLOB data is based on the declared column
// type and not the underlying data type. Any BLOB data in a non-BLOB column
// will cause a fault in this implementation. You are strongly recommended to
// use STRICT tables to avoid accidental BLOB data in a non-BLOB column.
type TriggerEngine struct {
	db           *sql.DB
	meta         *dbMeta
	handler      ChangesHandler
	tables       []string
	fnOnce       *sync.Once
	signal       Signal
	closed       chan any
	closeOnce    *sync.Once
	logTableName string
	maxBatchSize int
	subsec       bool
	blobs        bool
}

func (c *TriggerEngine) CDC(ctx context.Context) error {
	var err error
	c.fnOnce.Do(func() {
		err = c.cdc(ctx)
	})
	return err
}

func (c *TriggerEngine) cdc(ctx context.Context) error {
    log.Printf("CDC: Starting signal monitoring")
    if err := c.signal.Start(ctx); err != nil {
        log.Printf("CDC: Failed to start signal: %v", err)
        return fmt.Errorf("failed to start signal: %w", err)
    }
    
    log.Printf("CDC: Signal started successfully, getting waker channel")
    waker := c.signal.Waker()
    log.Printf("CDC: Entering main event loop")
    
    // Add counter to track iterations
    loopCount := 0
    
    for {
        loopCount++
        log.Printf("CDC: Loop iteration #%d, waiting for events", loopCount)
        
        select {
        case <-c.closed:
            log.Printf("CDC: Received closed signal, exiting cleanly")
            return nil
            
        case <-ctx.Done():
            log.Printf("CDC: Context canceled, calling Close")
            return c.Close(ctx)
            
        case event, ok := <-waker:
            if !ok {
                log.Printf("CDC: Waker channel closed, calling Close")
                return c.Close(ctx)
            }
            
            log.Printf("CDC: Received wake event: error=%v, wake=%v", event.Err, event.Wake)
            
            if event.Err != nil {
                log.Printf("CDC: Wake event has error, closing and returning error")
                _ = c.Close(ctx)
                return fmt.Errorf("wake signal error: %w", event.Err)
            }
            
            if !event.Wake {
                log.Printf("CDC: Wake event not requesting wake, continuing")
                continue
            }
            
            log.Printf("CDC: Calling drainChanges to process log entries")
            startTime := time.Now()
            
            if err := c.drainChanges(ctx); err != nil {
                log.Printf("CDC: drainChanges failed with error: %v", err)
                return fmt.Errorf("%w: failed to process changes from the log", err)
            }
            
            duration := time.Since(startTime)
            log.Printf("CDC: drainChanges completed successfully in %v", duration)
        }
    }
}

func (c *TriggerEngine) drainChanges(ctx context.Context) error {
    fmt.Printf("drainChanges: Starting with maxBatchSize=%d, log table=%s\n", c.maxBatchSize, c.logTableName)
    changes := make(Changes, 0, c.maxBatchSize)
    batchCounter := 0
    
    for {
        batchCounter++
        fmt.Printf("drainChanges: Starting batch #%d\n", batchCounter)
        
        // Construct the query string for clarity in logs
        query := fmt.Sprintf("SELECT id, timestamp, tablename, operation, before, after FROM %s ORDER BY id ASC LIMIT %d", 
                            c.logTableName, c.maxBatchSize)
        fmt.Printf("drainChanges: Executing query: %s\n", query)
        
        // Execute the query
        rows, err := c.db.QueryContext(ctx, query, c.maxBatchSize)
        if err != nil {
            fmt.Printf("drainChanges: ERROR querying log table: %v\n", err)
            return fmt.Errorf("%w: failed to select changes from the log", err)
        }
        defer rows.Close()
        
        // Initialize variables for tracking
        maxID := new(int64)
        rowCounter := 0
        fmt.Printf("drainChanges: Beginning to scan rows\n")
        
        // Process each row
        for rows.Next() {
            rowCounter++
            
            // Create variables to store column data
            timestamp := new(string)
            table := new(string)
            operation := new(string)
            before := &sql.NullString{}
            after := &sql.NullString{}
            
            // Scan the current row
            if err := rows.Scan(maxID, timestamp, table, operation, before, after); err != nil {
                fmt.Printf("drainChanges: ERROR scanning row: %v\n", err)
                return fmt.Errorf("%w: failed to read change record from the log", err)
            }
            
            fmt.Printf("drainChanges: Row %d scanned successfully: id=%d, table=%s, op=%s, timestamp=%s\n", 
                      rowCounter, *maxID, *table, *operation, *timestamp)
            
            // Parse timestamp
            ts, err := time.Parse("2006-01-02 15:04:05.999999999", *timestamp)
            if err != nil {
                fmt.Printf("drainChanges: ERROR parsing timestamp '%s': %v\n", *timestamp, err)
                return fmt.Errorf("%w: failed to parse timestamp %s from the log", err, *timestamp)
            }
            
            // Create change object
            ch := Change{
                Timestamp: ts,
                Table:     *table,
                Operation: strToOperation(*operation),
            }
            
            // Add before/after data if available
            if before.Valid {
                beforeLen := len(before.String)
                if beforeLen > 50 {
                    fmt.Printf("drainChanges: Adding 'before' data (%d bytes, starts with: %.50s...)\n", 
                              beforeLen, before.String)
                } else {
                    fmt.Printf("drainChanges: Adding 'before' data (%d bytes): %s\n", 
                              beforeLen, before.String)
                }
                ch.Before = []byte(before.String)
            } else {
                fmt.Printf("drainChanges: No 'before' data for this change\n")
            }
            
            if after.Valid {
                afterLen := len(after.String)
                if afterLen > 50 {
                    fmt.Printf("drainChanges: Adding 'after' data (%d bytes, starts with: %.50s...)\n", 
                              afterLen, after.String)
                } else {
                    fmt.Printf("drainChanges: Adding 'after' data (%d bytes): %s\n", 
                              afterLen, after.String)
                }
                ch.After = []byte(after.String)
            } else {
                fmt.Printf("drainChanges: No 'after' data for this change\n")
            }
            
            // Add to changes collection
            changes = append(changes, ch)
            fmt.Printf("drainChanges: Added change to batch, now have %d changes\n", len(changes))
        }
        
        // Check for errors during iteration
        if err := rows.Err(); err != nil {
            fmt.Printf("drainChanges: ERROR during row iteration: %v\n", err)
            return fmt.Errorf("%w: failed to read changes from the log", err)
        }
        
        fmt.Printf("drainChanges: Completed scanning %d rows\n", rowCounter)
        
        // If no changes were found, exit the function
        if len(changes) < 1 {
            fmt.Printf("drainChanges: No changes found in this batch, exiting function\n")
            return nil
        }
        
        // Handle the changes
        fmt.Printf("drainChanges: About to handle %d changes with maxID=%d\n", len(changes), *maxID)
        handlerStart := time.Now()
        if err := c.handle(ctx, changes); err != nil {
            fmt.Printf("drainChanges: ERROR in handler: %v\n", err)
            return fmt.Errorf("%w: failed to handle changes", err)
        }
        handlerDuration := time.Since(handlerStart)
        fmt.Printf("drainChanges: Successfully handled changes in %v\n", handlerDuration)
        
        // Clear the changes slice for reuse
        changes = changes[:0]
        fmt.Printf("drainChanges: Cleared changes slice for next batch\n")
        
        // Start transaction to delete processed records
        fmt.Printf("drainChanges: Starting transaction to delete processed logs\n")
        txStart := time.Now()
        tx, err := c.db.BeginTx(ctx, nil)
        if err != nil {
            fmt.Printf("drainChanges: ERROR beginning transaction: %v\n", err)
            return fmt.Errorf("%w: failed to create transaction to delete logs", err)
        }
        defer tx.Rollback()
        
        // Delete processed records
        deleteQuery := fmt.Sprintf("DELETE FROM %s WHERE id <= %d", c.logTableName, *maxID)
        fmt.Printf("drainChanges: Executing delete: %s\n", deleteQuery)
        deleteStart := time.Now()
        result, err := tx.ExecContext(ctx, deleteQuery, *maxID)
        if err != nil {
            fmt.Printf("drainChanges: ERROR deleting records: %v\n", err)
            return fmt.Errorf("%w: failed to delete handled logs", err)
        }
        
        // Get number of affected rows if possible
        if rowsAffected, err := result.RowsAffected(); err == nil {
            fmt.Printf("drainChanges: Deleted %d records\n", rowsAffected)
        } else {
            fmt.Printf("drainChanges: Deleted records, but couldn't get count: %v\n", err)
        }
        
        // Commit the transaction
        fmt.Printf("drainChanges: Committing transaction\n")
        if err = tx.Commit(); err != nil {
            fmt.Printf("drainChanges: ERROR committing transaction: %v\n", err)
            return fmt.Errorf("%w: failed to commit deletion of logs", err)
        }
        
        txDuration := time.Since(txStart)
        deleteDuration := time.Since(deleteStart)
        fmt.Printf("drainChanges: Successfully committed transaction in %v (delete operation: %v)\n", 
                  txDuration, deleteDuration)
        
        fmt.Printf("drainChanges: Completed batch #%d, starting next iteration\n", batchCounter)
    }
}

func (c *TriggerEngine) Bootstrap(ctx context.Context) error {
	var err error
	c.fnOnce.Do(func() {
		err = c.bootstrap(ctx)
	})
	return err
}

func (c *TriggerEngine) bootstrap(ctx context.Context) error {
	for _, table := range c.tables {
		if err := c.bootstrapTable(ctx, table); err != nil {
			return fmt.Errorf("%w: failed to bootstrap table %s", err, table)
		}
	}
	return nil
}

func (c *TriggerEngine) bootstrapTable(ctx context.Context, table string) error {
	t, ok := c.meta.Tables[table]
	if !ok {
		return fmt.Errorf("table %q not found in database", table)
	}
	q := sqlSelectFirst(t, c.blobs)
	rows, err := c.db.QueryContext(ctx, q, c.maxBatchSize-1)
	if err != nil {
		return fmt.Errorf("%w: failed to select first bootstrap rows for %s", err, table)
	}
	defer rows.Close()
	chs := make(Changes, 0, c.maxBatchSize)
	chs = append(chs, Change{
		Table:     table,
		Timestamp: time.Now(),
		Operation: Bootstrap,
	})
	selections := append(sqlKeyValuesForTable(t), new(string))
	for rows.Next() {
		if err := rows.Scan(selections...); err != nil {
			return fmt.Errorf("%w: failed to read bootstrap row for %s", err, table)
		}
		body := selections[len(selections)-1].(*string)
		chs = append(chs, Change{
			Table:     table,
			Timestamp: time.Now(),
			Operation: Insert,
			After:     []byte(*body),
		})
	}
	if rows.Err() != nil {
		return fmt.Errorf("%w: failed to read bootstrap rows for %s", rows.Err(), table)
	}
	_ = rows.Close()
	if len(chs) < 1 {
		return nil
	}
	if len(chs) < c.maxBatchSize {
		return c.handle(ctx, chs)
	}
	if err := c.handle(ctx, chs); err != nil {
		return fmt.Errorf("%w: failed to handle bootstrap changes for %s", err, table)
	}
	keys := make([]any, len(selections)-1)
	copy(keys, selections[:len(selections)-1])
	params := make([]any, len(keys)+1)
	for {
		q = sqlSelectNext(t, c.blobs)
		copy(params, keys)
		params[len(params)-1] = c.maxBatchSize
		rows, err = c.db.QueryContext(ctx, q, params...)
		if err != nil {
			return fmt.Errorf("%w: failed to select bootstrap rows for %s", err, table)
		}
		defer rows.Close()
		chs = chs[:0]
		for rows.Next() {
			selections = append(sqlKeyValuesForTable(t), new(string))
			if err := rows.Scan(selections...); err != nil {
				return fmt.Errorf("%w: failed to read bootstrap row for %s", err, table)
			}
			body := selections[len(selections)-1].(*string)
			chs = append(chs, Change{
				Table:     table,
				Timestamp: time.Now(),
				Operation: Insert,
				After:     []byte(*body),
			})
			copy(keys, selections[:len(selections)-1])
		}
		if rows.Err() != nil {
			return fmt.Errorf("%w: failed to read bootstrap rows for %s", rows.Err(), table)
		}
		_ = rows.Close()
		if len(chs) < 1 {
			return nil
		}
		if len(chs) < c.maxBatchSize {
			return c.handle(ctx, chs)
		}
		if err := c.handle(ctx, chs); err != nil {
			return fmt.Errorf("%w: failed to handle bootstrap changes for %s", err, table)
		}
	}
}

func (c *TriggerEngine) BootstrapAndCDC(ctx context.Context) error {
	var err error
	c.fnOnce.Do(func() {
		err = c.bootstrap(ctx)
		if err != nil {
			return
		}
		err = c.cdc(ctx)
	})
	return err
}
func (c *TriggerEngine) Setup(ctx context.Context) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: failed to create setup transaction", err)
	}
	defer tx.Rollback()

	logSQL := sqlCreateLogTable(c.logTableName)
	if _, err = tx.Exec(logSQL); err != nil {
		return fmt.Errorf("%w: failed to create log table", err)
	}
	for _, table := range c.tables {
		t, ok := c.meta.Tables[table]
		if !ok {
			return fmt.Errorf("table %q not found in database", table)
		}
		if _, err = tx.Exec(sqlCreateTableTriggerInsert(c.logTableName, t, c.subsec, c.blobs)); err != nil {
			return fmt.Errorf("%w: failed to create table trigger for inserts on %s", err, table)
		}
		if _, err = tx.Exec(sqlCreateTableTriggerUpdate(c.logTableName, t, c.subsec, c.blobs)); err != nil {
			return fmt.Errorf("%w: failed to create table trigger for updates on %s", err, table)
		}
		if _, err = tx.Exec(sqlCreateTableTriggerDelete(c.logTableName, t, c.subsec, c.blobs)); err != nil {
			return fmt.Errorf("%w: failed to create table trigger for deletes on %s", err, table)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("%w: failed to commit setup transaction", err)
	}
	return nil
}
func (c *TriggerEngine) Teardown(ctx context.Context) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: failed to create teardown transaction", err)
	}
	defer tx.Rollback()

	for _, table := range c.tables {
		t, ok := c.meta.Tables[table]
		if !ok {
			return fmt.Errorf("table %q not found in database", table)
		}
		if _, err = tx.Exec(sqlDeleteTableTriggerInsert(t)); err != nil {
			return fmt.Errorf("%w: failed to delete table trigger for inserts on %s", err, table)
		}
		if _, err = tx.Exec(sqlDeleteTableTriggerUpdate(t)); err != nil {
			return fmt.Errorf("%w: failed to delete table trigger for updates on %s", err, table)
		}
		if _, err = tx.Exec(sqlDeleteTableTriggerDelete(t)); err != nil {
			return fmt.Errorf("%w: failed to delete table trigger for deletes on %s", err, table)
		}
	}
	logSQL := sqlDeleteLogTable(c.logTableName)
	if _, err = tx.Exec(logSQL); err != nil {
		return fmt.Errorf("%w: failed to delete log table", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("%w: failed to commit teardown transaction", err)
	}
	return nil
}
func (c *TriggerEngine) Close(ctx context.Context) error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		if c.signal != nil {
			if cerr := c.signal.Close(); cerr != nil {
				err = fmt.Errorf("failed to close wake signal: %w", cerr)
				return
			}
		}
	})
	return err
}

func (c *TriggerEngine) handle(ctx context.Context, changes Changes) error {
	return c.handler.HandleChanges(ctx, changes)
}

func sqlCreateLogTable(name string) string {
	return `CREATE TABLE IF NOT EXISTS ` + name + ` (
		id INTEGER PRIMARY KEY,
		timestamp TEXT NOT NULL,
		tablename TEXT NOT NULL,
		operation TEXT NOT NULL,
		before TEXT,
		after TEXT
	)`
}
func sqlCreateTableTriggerInsert(logTable string, table tableMeta, subsec bool, blobs bool) string {
	return `CREATE TRIGGER IF NOT EXISTS ` + table.Name + `__cdc_insert AFTER INSERT ON ` + table.Name + ` BEGIN
		INSERT INTO ` + logTable + ` (timestamp, tablename, operation, before, after) VALUES
			(` + sqlDateTimeNow(subsec) + `, '` + table.Name + `', 'INSERT', NULL, ` + sqlJsonObject("NEW.", table.Columns, blobs) + `);
	END`
}
func sqlCreateTableTriggerUpdate(logTable string, table tableMeta, subsec bool, blobs bool) string {
	return `CREATE TRIGGER IF NOT EXISTS ` + table.Name + `__cdc_update AFTER UPDATE ON ` + table.Name + ` BEGIN
		INSERT INTO ` + logTable + ` (timestamp, tablename, operation, before, after) VALUES
			(` + sqlDateTimeNow(subsec) + `, '` + table.Name + `', 'UPDATE', ` + sqlJsonObject("OLD.", table.Columns, blobs) + `, ` + sqlJsonObject("NEW.", table.Columns, blobs) + `);
	END`
}
func sqlCreateTableTriggerDelete(logTable string, table tableMeta, subsec bool, blobs bool) string {
	return `CREATE TRIGGER IF NOT EXISTS ` + table.Name + `__cdc_delete AFTER DELETE ON ` + table.Name + ` BEGIN
		INSERT INTO ` + logTable + ` (timestamp, tablename, operation, before, after) VALUES
			(` + sqlDateTimeNow(subsec) + `, '` + table.Name + `', 'DELETE', ` + sqlJsonObject("OLD.", table.Columns, blobs) + `, NULL);
	END`
}
func sqlDateTimeNow(subsec bool) string {
	if subsec {
		return "datetime('now', 'subsec')"
	}
	return "datetime('now')"
}
func sqlDeleteTableTriggerInsert(table tableMeta) string {
	return `DROP TRIGGER IF EXISTS ` + table.Name + `__cdc_insert`
}
func sqlDeleteTableTriggerUpdate(table tableMeta) string {
	return `DROP TRIGGER IF EXISTS ` + table.Name + `__cdc_update`
}
func sqlDeleteTableTriggerDelete(table tableMeta) string {
	return `DROP TRIGGER IF EXISTS ` + table.Name + `__cdc_delete`
}
func sqlDeleteLogTable(table string) string {
	return `DROP TABLE IF EXISTS ` + table
}

const colChunkSize = 63

func sqlJsonObject(prefix string, columns []columnMeta, blobs bool) string {
	objects := make([]string, 0, (len(columns)/colChunkSize)+1)
	var b strings.Builder
	b.WriteString("json_object(")
	for offset, column := range columns {
		if strings.ToUpper(column.Type) == "BLOB" {
			if blobs {
				b.WriteString(fmt.Sprintf("'%s'", column.Name))
				b.WriteString(", hex(")
				b.WriteString(prefix + column.Name)
				b.WriteString(")")
				if (offset+1)%colChunkSize == 0 && offset < len(columns)-1 {
					b.WriteString(")")
					objects = append(objects, b.String())
					b.Reset()
					b.WriteString("json_object(")
					continue
				}
				if offset < len(columns)-1 {
					b.WriteString(", ")
				}
			}
			continue
		}
		b.WriteString(fmt.Sprintf("'%s'", column.Name))
		b.WriteString(", ")
		b.WriteString(prefix + column.Name)

		if (offset+1)%colChunkSize == 0 && offset < len(columns)-1 {
			b.WriteString(")")
			objects = append(objects, b.String())
			b.Reset()
			b.WriteString("json_object(")
			continue
		}
		if offset < len(columns)-1 {
			b.WriteString(", ")
		}
	}
	b.WriteString(")")
	objects = append(objects, b.String())
	b.Reset()

	if len(objects) == 1 {
		return objects[0]
	}
	for offset, object := range objects {
		if offset+1 == len(objects) {
			b.WriteString(object)
			for x := 1; x < len(objects); x = x + 1 {
				b.WriteString(")")
			}
			continue
		}
		b.WriteString("json_patch(")
		b.WriteString(object)
		b.WriteString(", ")
	}
	return b.String()
}

func sqlSelectFirst(table tableMeta, blobs bool) string {
	if !table.WithoutRowID {
		return `SELECT rowid, ` + sqlJsonObject("", table.Columns, blobs) + ` AS body FROM ` + table.Name + ` ORDER BY rowid LIMIT ?`
	}
	var keyCount int
	for _, column := range table.Columns {
		if column.PK != 0 {
			keyCount = keyCount + 1
		}
	}
	keyColumns := make([]string, keyCount)
	for _, column := range table.Columns {
		if column.PK != 0 {
			keyColumns[column.PK-1] = column.Name
		}
	}
	return `SELECT ` + strings.Join(keyColumns, ", ") + `, ` + sqlJsonObject("", table.Columns, blobs) + ` AS body FROM ` + table.Name + ` ORDER BY ` + strings.Join(keyColumns, ", ") + ` LIMIT ?`
}

func sqlSelectNext(table tableMeta, blobs bool) string {
	if !table.WithoutRowID {
		return `SELECT rowid, ` + sqlJsonObject("", table.Columns, blobs) + ` AS body FROM ` + table.Name + ` WHERE rowid > ? ORDER BY rowid LIMIT ?`
	}
	var keyCount int
	for _, column := range table.Columns {
		if column.PK != 0 {
			keyCount = keyCount + 1
		}
	}
	keyColumns := make([]string, keyCount)
	for _, column := range table.Columns {
		if column.PK != 0 {
			keyColumns[column.PK-1] = column.Name
		}
	}
	var b strings.Builder
	b.WriteString(`SELECT ` + strings.Join(keyColumns, ", ") + `, ` + sqlJsonObject("", table.Columns, blobs) + ` AS body FROM ` + table.Name)
	b.WriteString(` WHERE `)
	for offset, column := range keyColumns {
		b.WriteString(column)
		b.WriteString(" > ?")
		if offset < keyCount-1 {
			b.WriteString(" AND ")
		}
	}
	b.WriteString(` ORDER BY ` + strings.Join(keyColumns, ", ") + ` LIMIT ?`)

	return b.String()
}

func sqlKeyValuesForTable(table tableMeta) []any {
	if !table.WithoutRowID {
		return []any{new(int64)}
	}
	var keyCount int
	for _, column := range table.Columns {
		if column.PK != 0 {
			keyCount = keyCount + 1
		}
	}
	keyValues := make([]any, keyCount)
	for offset, column := range table.Columns {
		if column.PK != 0 {
			keyValues[offset] = new(any)
		}
	}
	return keyValues
}

func strToOperation(operation string) Operation {
	switch strings.ToUpper(operation) {
	case "INSERT":
		return Insert
	case "UPDATE":
		return Update
	case "DELETE":
		return Delete
	}
	return Operation("UNKNOWN")
}
