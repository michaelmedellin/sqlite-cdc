// SPDX-FileCopyrightText: © 2024 Kevin Conway <kevin@conway0.com>
// SPDX-License-Identifier: Apache-2.0

package cdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestBootstrapWithRowID(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	defer db.Close()

	count := 1024
	createTable(t, db)
	generateRecords(t, db, count, 0)

	h := newHandler()
	batchSize := defaultMaxBatchSize
	c, err := NewTriggerEngine(db, h, []string{testTableName}, WithMaxBatchSize(batchSize), WithBlobSupport(true))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, c.Bootstrap(ctx))
	require.NoError(t, c.Close(ctx))

	expectedBatches := count / batchSize
	if count%batchSize != 0 {
		expectedBatches = expectedBatches + 1
	}
	waitForChanges(t, h, expectedBatches, count, time.Second)
}

func TestCDCWithRowID(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	t.Cleanup(func() { db.Close() })

	count := 1024
	createTable(t, db)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	h := newHandler()
	batchSize := defaultMaxBatchSize
	fsSignal, err := NewFSNotifySignal(db)
	require.NoError(t, err)
	timeSignal, err := NewTimeSignal(10 * time.Millisecond)
	require.NoError(t, err)
	signal, err := NewMultiSignal(fsSignal, timeSignal)
	require.NoError(t, err)
	c, err := NewTriggerEngine(db, h, []string{testTableName}, WithMaxBatchSize(batchSize), WithBlobSupport(true), WithSignal(signal))
	require.NoError(t, err)
	t.Cleanup(func() { c.Close(ctx) })

	require.NoError(t, c.Setup(ctx))

	cdcStatus := make(chan error, 1)
	go func(t *testing.T, c CDC) {
		t.Helper()
		cdcStatus <- c.CDC(ctx)
	}(t, c)
	time.Sleep(5 * time.Millisecond) // force a scheduler break to get CDC going
	generateRecords(t, db, count, 0)

	expectedBatches := count / batchSize
	if count%batchSize != 0 {
		expectedBatches = expectedBatches + 1
	}
	waitForChanges(t, h, expectedBatches, count, time.Second)
	require.NoError(t, c.Close(ctx))
	require.NoError(t, <-cdcStatus)
}

func TestBootstrapWithoutRowID(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	defer db.Close()

	count := 1024
	createTableWithoutRowID(t, db)
	generateRecords(t, db, count, 0)

	h := newHandler()
	batchSize := defaultMaxBatchSize
	c, err := NewTriggerEngine(db, h, []string{testTableName}, WithMaxBatchSize(batchSize), WithBlobSupport(true))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, c.Bootstrap(ctx))
	require.NoError(t, c.Close(ctx))

	expectedBatches := count / batchSize
	if count%batchSize != 0 {
		expectedBatches = expectedBatches + 1
	}

	results := h.Changes()
	require.Len(t, results, expectedBatches)
	totalChanges := 0
	for _, changes := range results {
		totalChanges = totalChanges + len(changes)
	}
	require.Equal(t, count+1, totalChanges) // +1 for the BOOTSTRAP event
}

func TestCDCWithoutRowID(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	t.Cleanup(func() { db.Close() })

	count := 1024
	createTableWithoutRowID(t, db)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	h := newHandler()
	batchSize := defaultMaxBatchSize
	fsSignal, err := NewFSNotifySignal(db)
	require.NoError(t, err)
	timeSignal, err := NewTimeSignal(10 * time.Millisecond)
	require.NoError(t, err)
	signal, err := NewMultiSignal(fsSignal, timeSignal)
	require.NoError(t, err)
	c, err := NewTriggerEngine(db, h, []string{testTableName}, WithMaxBatchSize(batchSize), WithBlobSupport(true), WithSignal(signal))
	require.NoError(t, err)
	t.Cleanup(func() { c.Close(ctx) })

	require.NoError(t, c.Setup(ctx))

	cdcStatus := make(chan error, 1)
	go func(t *testing.T, c CDC) {
		t.Helper()
		cdcStatus <- c.CDC(ctx)
	}(t, c)

	time.Sleep(5 * time.Millisecond) // force a scheduler break to get CDC going
	generateRecords(t, db, count, 0)

	expectedBatches := count / batchSize
	if count%batchSize != 0 {
		expectedBatches = expectedBatches + 1
	}
	waitForChanges(t, h, expectedBatches, count, time.Second)
	require.NoError(t, c.Close(ctx))
	require.NoError(t, <-cdcStatus)
}

func TestBootstrapAndCDCWithRowID(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	t.Cleanup(func() { db.Close() })

	count := 1024
	createTable(t, db)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	h := newHandler()
	batchSize := defaultMaxBatchSize
	fsSignal, err := NewFSNotifySignal(db)
	require.NoError(t, err)
	timeSignal, err := NewTimeSignal(10 * time.Millisecond)
	require.NoError(t, err)
	signal, err := NewMultiSignal(fsSignal, timeSignal)
	require.NoError(t, err)
	c, err := NewTriggerEngine(db, h, []string{testTableName}, WithMaxBatchSize(batchSize), WithBlobSupport(true), WithSignal(signal))
	require.NoError(t, err)
	t.Cleanup(func() { c.Close(ctx) })

	require.NoError(t, c.Setup(ctx))
	generateRecords(t, db, count, 0)

	cdcStatus := make(chan error, 1)
	go func(t *testing.T, c CDC) {
		t.Helper()
		cdcStatus <- c.BootstrapAndCDC(ctx)
	}(t, c)

	time.Sleep(5 * time.Millisecond) // force a scheduler break to get CDC going
	generateRecords(t, db, count, count)
	expectedBatches := (count * 2) / batchSize
	if (count*2)%batchSize != 0 {
		expectedBatches = expectedBatches + 1
	}
	waitForChanges(t, h, expectedBatches, count*2, time.Second)
	require.NoError(t, c.Close(ctx))
	require.NoError(t, <-cdcStatus)
}

func TestBootstrapAndCDCWithoutRowID(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	t.Cleanup(func() { db.Close() })

	count := 1024
	createTableWithoutRowID(t, db)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	h := newHandler()
	batchSize := defaultMaxBatchSize
	fsSignal, err := NewFSNotifySignal(db)
	require.NoError(t, err)
	timeSignal, err := NewTimeSignal(10 * time.Millisecond)
	require.NoError(t, err)
	signal, err := NewMultiSignal(fsSignal, timeSignal)
	require.NoError(t, err)
	c, err := NewTriggerEngine(db, h, []string{testTableName}, WithMaxBatchSize(batchSize), WithBlobSupport(true), WithSignal(signal))
	require.NoError(t, err)
	t.Cleanup(func() { c.Close(ctx) })

	require.NoError(t, c.Setup(ctx))
	generateRecords(t, db, count, 0)

	cdcStatus := make(chan error, 1)
	go func(t *testing.T, c CDC) {
		t.Helper()
		cdcStatus <- c.BootstrapAndCDC(ctx)
	}(t, c)

	time.Sleep(5 * time.Millisecond) // force a scheduler break to get CDC going
	generateRecords(t, db, count, count)
	expectedBatches := (count * 2) / batchSize
	if (count*2)%batchSize != 0 {
		expectedBatches = expectedBatches + 1
	}
	waitForChanges(t, h, expectedBatches, count*2, time.Second)
	require.NoError(t, c.Close(ctx))
	require.NoError(t, <-cdcStatus)
}

func TestWideTables(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	defer db.Close()

	columnCount := 1000 // This is the default max stack depth in SQLite
	var b strings.Builder
	b.WriteString("CREATE TABLE test (")
	for x := 0; x < columnCount; x = x + 1 {
		b.WriteString(fmt.Sprintf("col%d INT", x))
		if x < columnCount-1 {
			b.WriteString(", ")
		}
	}
	b.WriteString(")")
	_, err := db.Exec(b.String())
	require.NoError(t, err)

	b.Reset()
	params := make([]any, columnCount)
	b.WriteString("INSERT INTO test VALUES (")
	for x := 0; x < columnCount; x = x + 1 {
		params[x] = x
		b.WriteString("?")
		if x < columnCount-1 {
			b.WriteString(", ")
		}
	}
	b.WriteString(")")
	_, err = db.Exec(b.String(), params...)
	require.NoError(t, err)

	h := newHandler()
	batchSize := defaultMaxBatchSize
	c, err := NewTriggerEngine(db, h, []string{testTableName}, WithMaxBatchSize(batchSize), WithBlobSupport(true))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, c.Bootstrap(ctx))
	require.NoError(t, c.Close(ctx))

	results := h.Changes()
	require.Len(t, results, 1)
	require.Len(t, results[0], 2)
	ch := results[0][0]
	require.Equal(t, Bootstrap, ch.Operation)
	ch = results[0][1]
	afterMap := make(map[string]any)
	require.NoError(t, json.Unmarshal(ch.After, &afterMap))
	require.Len(t, afterMap, columnCount)
}

var (
	smallColumnCounts = []int{2, 4, 8, 16, 32, 63}     //nolint:gochecknoglobals
	largeColumnCounts = []int{64, 128, 256, 512, 1000} //nolint:gochecknoglobals
)

// This benchmark measures the added latency of the CDC triggers for inserts.
//
// The tables used in this benchmark have a simplistic structure where all
// columns are integer types. The number of columns varies by the test case but
// never exceeds the 63 column limit for generating a change event in a single
// step. Each table is tested with the triggers on and off to highlight the
// added latency of the triggers.
//
// All of the writes are applied serially so there is no impact from concurrent
// writes.
func BenchmarkTriggerLatencySimpleTableSerialInserts(b *testing.B) {
	for _, columnCount := range smallColumnCounts {
		benchmarkTriggerLatencyTableSerial(b, columnCount, OpInsert)
	}
}

// This benchmark measures the added latency of the CDC triggers for updates.
//
// The tables used in this benchmark have a simplistic structure where all
// columns are integer types. The number of columns varies by the test case but
// never exceeds the 63 column limit for generating a change event in a single
// step. Each table is tested with the triggers on and off to highlight the
// added latency of the triggers.
//
// The table is populated with enough records that each step of the benchmark
// operates on a unique row. All columns in each row are updated in each step.
//
// All of the writes are applied serially so there is no impact from concurrent
// writes.
func BenchmarkTriggerLatencySimpleTableSerialUpdates(b *testing.B) {
	for _, columnCount := range smallColumnCounts {
		benchmarkTriggerLatencyTableSerial(b, columnCount, OpUpdate)
	}
}

// This benchmark measures the added latency of the CDC triggers for deletes.
//
// The tables used in this benchmark have a simplistic structure where all
// columns are integer types. The number of columns varies by the test case but
// never exceeds the 63 column limit for generating a change event in a single
// step. Each table is tested with the triggers on and off to highlight the
// added latency of the triggers.
//
// The table is populated with enough records that each step of the benchmark
// operates on a unique row. Each step deletes a row.
//
// All of the writes are applied serially so there is no impact from concurrent
// writes.
func BenchmarkTriggerLatencySimpleTableSerialDeletes(b *testing.B) {
	for _, columnCount := range smallColumnCounts {
		benchmarkTriggerLatencyTableSerial(b, columnCount, OpDelete)
	}
}

// Measure the latency of inserts in wide tables.
//
// The tables used in this benchmark have a simplistic structure where all
// columns are integer types. The number of columns varies by the test case but
// always exceeds the 63 column limit for generating a change event in a single
// step. Each table is tested with the triggers on and off to highlight the
// added latency of the triggers.
//
// All of the writes are applied serially so there is no impact from concurrent
// writes.
func BenchmarkTriggerLatencyWideTableSerialInserts(b *testing.B) {
	for _, columnCount := range largeColumnCounts {
		benchmarkTriggerLatencyTableSerial(b, columnCount, OpInsert)
	}
}

// Measure the latency of updates in wide tables.
//
// The tables used in this benchmark have a simplistic structure where all
// columns are integer types. The number of columns varies by the test case but
// always exceeds the 63 column limit for generating a change event in a single
// step. Each table is tested with the triggers on and off to highlight the
// added latency of the triggers.
//
// The table is populated with enough records that each step of the benchmark
// operates on a unique row. All columns in each row are updated in each step.
//
// All of the writes are applied serially so there is no impact from concurrent
// writes.
func BenchmarkTriggerLatencyWideTableSerialUpdates(b *testing.B) {
	for _, columnCount := range largeColumnCounts {
		benchmarkTriggerLatencyTableSerial(b, columnCount, OpUpdate)
	}
}

// Measure the latency of deletes in wide tables.
//
// The tables used in this benchmark have a simplistic structure where all
// columns are integer types. The number of columns varies by the test case but
// always exceeds the 63 column limit for generating a change event in a single
// step. Each table is tested with the triggers on and off to highlight the
// added latency of the triggers.
//
// The table is populated with enough records that each step of the benchmark
// operates on a unique row. Each step deletes a row.
//
// All of the writes are applied serially so there is no impact from concurrent
// writes.
func BenchmarkTriggerLatencyWideTableSerialDeletes(b *testing.B) {
	for _, columnCount := range largeColumnCounts {
		benchmarkTriggerLatencyTableSerial(b, columnCount, OpDelete)
	}
}

// This benchmark measures the BLOB column type encoding process.
//
// BLOB type columns have to be encoded because JSON does not have a native
// binary type. The encoding process uses the hex encoding SQLite function.
// This benchmark attempts to measure the encoding time growth as the size of
// the blob increases.
func BenchmarkBlobEncoding(b *testing.B) {
	blobSizes := []int{16, 64, 256, 1024, 4096, 16384, 32768, 65536, 131072, 262144, 524288, 1048576}
	for _, blobSize := range blobSizes {
		b.Run(fmt.Sprintf("size=%d", blobSize), func(b *testing.B) {
			db := testDB(b)
			defer db.Close()

			_, err := db.Exec(`CREATE TABLE test (col BLOB)`)
			require.NoError(b, err)

			blobBody := make([]byte, blobSize)
			for x := 0; x < blobSize; x = x + 1 {
				blobBody[x] = byte(x % 256)
			}

			q := `INSERT INTO test VALUES (?)`
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := &handlerNull{}
			c, err := NewTriggerEngine(db, h, []string{testTableName}, WithBlobSupport(true))
			require.NoError(b, err)
			defer c.Close(ctx)
			require.NoError(b, c.Setup(ctx))

			b.ResetTimer()
			for n := 0; n < b.N; n = n + 1 {
				_, err = db.Exec(q, blobBody)
				require.NoError(b, err)
			}
		})
	}
}

type OpType string

const (
	OpInsert OpType = "insert"
	OpUpdate OpType = "update"
	OpDelete OpType = "delete"
)

func benchmarkTriggerLatencyTableSerial(b *testing.B, columnCount int, op OpType) {
	b.Helper()

	b.Run(fmt.Sprintf("triggers=off/columns=%d", columnCount), func(b *testing.B) {
		b.StopTimer()
		db := testDB(b)
		defer db.Close()

		query, params := setupBenchmarkTable(b, db, columnCount, op, b.N)

		b.ResetTimer()
		b.StartTimer()
		for n := 0; n < b.N; n = n + 1 {
			if op != OpInsert {
				params[len(params)-1] = n
			}
			if op == OpInsert {
				params[0] = n
			}
			_, err := db.Exec(query, params...)
			require.NoError(b, err)
		}
	})

	b.Run(fmt.Sprintf("triggers=on/columns=%d", columnCount), func(b *testing.B) {
		b.StopTimer()
		db := testDB(b)
		defer db.Close()

		query, params := setupBenchmarkTable(b, db, columnCount, op, b.N)

		c, err := NewTriggerEngine(db, &handlerNull{}, []string{testTableName}, WithBlobSupport(true))
		require.NoError(b, err)
		defer c.Close(context.Background())
		require.NoError(b, c.Setup(context.Background()))

		b.ResetTimer()
		b.StartTimer()
		for n := 0; n < b.N; n = n + 1 {
			if op != OpInsert {
				params[len(params)-1] = n
			}
			if op == OpInsert {
				params[0] = n
			}
			_, err = db.Exec(query, params...)
			require.NoError(b, err)
		}
	})
}

func setupBenchmarkTable(b *testing.B, db *sql.DB, columnCount int, op OpType, n int) (string, []any) {
	b.Helper()

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("CREATE TABLE %s (", testTableName))
	for x := 0; x < columnCount; x = x + 1 {
		builder.WriteString(fmt.Sprintf("col%d INT", x))
		if x == 0 {
			builder.WriteString(" PRIMARY KEY")
		}
		if x < columnCount-1 {
			builder.WriteString(", ")
		}
	}
	builder.WriteString(") WITHOUT ROWID")
	_, err := db.Exec(builder.String())
	require.NoError(b, err)

	// For updates and deletes, we need initial data
	if op != OpInsert {
		builder.Reset()
		params := make([]any, columnCount)
		builder.WriteString(fmt.Sprintf("INSERT INTO %s VALUES (", testTableName))
		for x := 0; x < columnCount; x = x + 1 {
			params[x] = x
			builder.WriteString("?")
			if x < columnCount-1 {
				builder.WriteString(", ")
			}
		}
		builder.WriteString(")")
		query := builder.String()

		// Insert a record count equal to the requested benchmark operation count
		for x := 0; x < n; x = x + 1 {
			params[0] = x
			_, err = db.Exec(query, params...)
			require.NoError(b, err)
		}
	}

	// Prepare the operation query
	var query string
	var params []any
	switch op {
	case OpInsert:
		builder.Reset()
		params = make([]any, columnCount)
		builder.WriteString(fmt.Sprintf("INSERT INTO %s VALUES (", testTableName))
		for x := 0; x < columnCount; x = x + 1 {
			params[x] = x
			builder.WriteString("?")
			if x < columnCount-1 {
				builder.WriteString(", ")
			}
		}
		builder.WriteString(")")
		query = builder.String()
	case OpUpdate:
		builder.Reset()
		params = make([]any, columnCount)
		builder.WriteString(fmt.Sprintf("UPDATE %s SET ", testTableName))
		for x := 1; x < columnCount; x = x + 1 {
			// Set all the columns to a value greater than the number of rows
			// inserted during setup. This ensures that all columns are modified
			// and that no row is modified twice.
			params[x-1] = n + 1
			builder.WriteString(fmt.Sprintf("col%d = ?", x))
			if x < columnCount-1 {
				builder.WriteString(", ")
			}
		}
		builder.WriteString(" WHERE col0 = ?")
		params[len(params)-1] = 0
		query = builder.String()
	case OpDelete:
		query = fmt.Sprintf("DELETE FROM %s WHERE col0 = ?", testTableName)
		params = make([]any, 1)
	}

	return query, params
}

func generateRecords(t tOrB, db *sql.DB, n int, offset int) {
	t.Helper()

	tx, err := db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()

	textValue := "foo"
	blobValue := []byte{0xDE, 0xAD, 0xBE, 0xAF}
	realValue := 3.14
	numericValue := 1
	for x := 0; x < n; x = x + 1 {
		intValue := x + offset
		_, err := tx.Exec(
			`INSERT INTO `+testTableName+` VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?
			)`,
			intValue, intValue, intValue, intValue, intValue, intValue, intValue, intValue, intValue,
			textValue, textValue, textValue, textValue, textValue, textValue, textValue, textValue,
			blobValue,
			realValue, realValue, realValue, realValue,
			numericValue, numericValue, numericValue, numericValue, numericValue,
		)
		require.NoError(t, err)
	}

	require.NoError(t, tx.Commit())
}

func createTable(t tOrB, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(sqlCreateTestTable)
	require.NoError(t, err)
}

func createTableWithoutRowID(t tOrB, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(sqlCreateTestTable + " WITHOUT ROWID")
	require.NoError(t, err)
}

const testTableName = "test"
const sqlCreateTestTable = `CREATE TABLE ` + testTableName + ` (
	a INT,
	b INTEGER,
	c TINYINT,
	d SMALLINT,
	e MEDIUMINT,
	f BIGINT,
	g UNSIGNED BIG INT,
	h INT2,
	i INT8,

	j CHARACTER(20),
	k VARCHAR(255),
	l VARYING CHARACTER(255),
	m NCHAR(55),
	n NATIVE CHARACTER(70),
	o NVARCHAR(100),
	p TEXT,
	q CLOB,

	r BLOB,

	s REAL,
	t DOUBLE,
	u DOUBLE PRECISION,
	v FLOAT,

	w NUMERIC,
	x DECIMAL(10,5),
	y BOOLEAN,
	z DATE,
	aa DATETIME,

	PRIMARY KEY (a,b,c)
)`

type tOrB interface {
	Errorf(format string, args ...interface{})
	FailNow()
	Helper()
	TempDir() string
}

type testCDC struct {
	db    *sql.DB
	cdc   CDC
	awake chan<- SignalEvent
}

func (c *testCDC) Cleanup() {
	_ = c.db.Close()
	_ = c.cdc.Close(context.Background())
}

func newTestCDC(t tOrB, handler ChangesHandler, options ...Option) *testCDC {
	t.Helper()
	db := testDB(t)
	awake := make(chan SignalEvent)
	signal, err := NewChannelSignal(awake)
	require.NoError(t, err)
	options = append(options, WithSignal(signal))
	cdc, err := NewTriggerEngine(db, handler, []string{testTableName}, options...)
	require.NoError(t, err)
	return &testCDC{
		db:    db,
		cdc:   cdc,
		awake: awake,
	}
}

func testDB(t tOrB) *sql.DB {
	t.Helper()
	dir := t.TempDir()

	db, err := sql.Open("sqlite", filepath.Join(dir, "test.sqlite")+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=synchronous(normal)&_pragma=foreign_keys(on)")
	require.NoError(t, err)
	return db
}

func waitForChanges(t tOrB, h *handler, expectedBatches int, expectedChanges int, timeout time.Duration) {
	t.Helper()

	results := make([]Changes, 0, expectedBatches)
	totalChanges := 0
	start := time.Now()
	didTimeout := false
	for len(results) < expectedBatches && totalChanges < expectedChanges {
		if time.Since(start) > timeout {
			didTimeout = true
			break
		}
		results = append(results, h.Changes()...)
		if len(results) > 0 {
			totalChanges = totalChanges + len(results[len(results)-1])
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.False(t, didTimeout, "CDC did not complete in time. wanted %d but got %d", expectedChanges, totalChanges)
}

type handler struct {
	changes []Changes
	lock    sync.Locker
}

func newHandler() *handler {
	return &handler{
		lock: &sync.Mutex{},
	}
}

func (h *handler) Changes() []Changes {
	h.lock.Lock()
	defer h.lock.Unlock()

	changes := h.changes
	h.changes = nil
	return changes
}

func (h *handler) HandleChanges(ctx context.Context, changes Changes) error {
	h.lock.Lock()
	defer h.lock.Unlock()
	h.changes = append(h.changes, changes)
	return nil
}

type handlerNull struct{}

func (h *handlerNull) HandleChanges(ctx context.Context, changes Changes) error {
	return nil
}
