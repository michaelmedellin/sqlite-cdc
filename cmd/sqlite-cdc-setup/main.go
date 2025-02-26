// SPDX-FileCopyrightText: © 2024 Kevin Conway <kevin@conway0.com>
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	cdc "github.com/michaelmedellin/sqlite-cdc"
	"github.com/michaelmedellin/sqlite-cdc/handlers"
)

type strList []string

func (l *strList) String() string {
	return strings.Join(*l, ",")
}

func (l *strList) Set(s string) error {
	*l = append(*l, s)
	return nil
}

type flags struct {
	dbFile        string
	dbParams      string
	tables        strList
	logTableName  string
	setup         bool
	teardown      bool
	disableSubsec bool
	blobs         bool
	version       bool
}

var (
	version = "source"                            //nolint:gochecknoglobals
	commit  = "unknown"                           //nolint:gochecknoglobals
	date    = time.Now().Format(time.RFC3339Nano) //nolint:gochecknoglobals
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, _ = signal.NotifyContext(ctx, os.Interrupt)

	f := flags{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&f.dbFile, "db", "", "SQLite file path")
	fs.StringVar(&f.dbParams, "db-params", "_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)", "SQLite connection parameters. See <https://pkg.go.dev/modernc.org/sqlite#Driver.Open> for parameter syntax")
	fs.StringVar(&f.logTableName, "log-table", "__cdc_log", "Name of the table to store CDC log entries")
	fs.Var(&f.tables, "table", "A table name to monitor. Can be specified multiple times")
	fs.BoolVar(&f.setup, "setup", false, "Perform initial setup of the database for CDC before starting in any mode")
	fs.BoolVar(&f.teardown, "teardown", false, "Perform teardown of the CDC tables and triggers. Setting the teardown flag prevents any other action. The process will perform the teardown and then exit")
	fs.BoolVar(&f.disableSubsec, "disable-subsec", false, "Disable subsecond time resolution to support old clients")
	fs.BoolVar(&f.blobs, "blobs", false, "Enable support for blobs")
	fs.BoolVar(&f.version, "version", false, "Print version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		log.Fatalln(err)
	}

	if f.version {
		fmt.Printf("%s version:%s commit:(%s)\n", os.Args[0], version, commit)
		t, err := time.Parse(time.RFC3339Nano, date)
		if err != nil {
			fmt.Printf("Built on %s\n", date)
			return
		}
		fmt.Printf("Built on %s\n", t.Format(time.RubyDate))
		return
	}

	if len(f.tables) < 1 {
		log.Fatalln("at least one table must be specified for setup or teardown operations")
	}
	if f.setup && f.teardown {
		log.Fatalln("setup and teardown flags are mutually exclusive")
	}

	dsn := fmt.Sprintf("%s?%s", f.dbFile, f.dbParams)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatalln(err)
	}
	defer db.Close()

	handler := &handlers.STDIO{Output: io.Discard}
	c, err := cdc.NewTriggerEngine(db, handler, f.tables,
		cdc.WithLogTableName(f.logTableName),
		cdc.WithoutSubsecondTime(f.disableSubsec),
		cdc.WithBlobSupport(f.blobs),
	)
	if err != nil {
		log.Fatalln(err)
	}

	if f.setup {
		if err = c.Setup(ctx); err != nil {
			log.Fatalln(err)
		}
		return
	}

	if f.teardown {
		if err = c.Teardown(ctx); err != nil {
			log.Fatalln(err)
		}
		return
	}

	<-ctx.Done()
}
