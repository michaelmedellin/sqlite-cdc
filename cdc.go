// SPDX-FileCopyrightText: © 2024 Kevin Conway <kevin@conway0.com>
// SPDX-License-Identifier: Apache-2.0

package cdc

import (
	"context"
)

// CDC represents a complete implementation of a CDC for SQLite.
//
// Installer methods should not be called if any Engine methods are running.
// Only one Engine method may be called per instance of CDC with the exception
// of Close which may be called at any time to end a running operation. The
// instance is no longer valid after an Engine method completes.
type CDC interface {
	Installer
	Engine
}

// Installer is an optional set of methods that a CDC implementation may offer.
//
// These methods must be present but may be no-ops if an implementation has no
// setup or teardown requirements. Generally, the setup method should be called
// before any Engine methods.
type Installer interface {
	// Perform any setup necessary to support CDC.
	Setup(ctx context.Context) error
	// Perform any teardown necessary to remove CDC.
	Teardown(ctx context.Context) error
}

// Engine represents the change handling portion of a CDC implementation.
//
// Only one of these methods may be called per instance of CDC with the
// exception of Close which may be called at any time to end a running
// operation. Once any of these methods returns then the instance is no longer
// valid.
type Engine interface {
	// CDC-only mode processes changes made to the database.
	//
	// This mode only operates on changes that have been captured by the CDC
	// implementation and does not process any unchanged records. This means
	// that CDC-only mode does not process existing, unmodified records like
	// Bootstrap does.
	//
	// This mode runs until stopped or it encounters an error.
	CDC(ctx context.Context) error
	// Bootstrap-only mode processes all existing records in the database.
	//
	// This mode only operates on the current state of existing records and does
	// not process any captured changes. All existing records are processed as
	// though they are INSERT operations. For convenience, the first change
	// handled by bootstrap mode for any table is always a BOOTSTRAP operation
	// with an empty before and after image. This signal may be used in systems
	// that manage state based on the stream of captured changes to indicate
	// that the previously accumulated state is likely invalid and must be
	// re-built from the bootstrap data.
	//
	// This mode runs until it completes a scan of each table and then exits.
	Bootstrap(ctx context.Context) error
	// Bootstrap-and-CDC mode is a combination of the bootstrap and CDC modes.
	//
	// This mode starts with a bootstrap and then enters CDC mode once it is
	// complete. Any changes to data during hte bootstrap are captured and
	// emitted once the engine enters CDC mode.
	//
	// This mode runs until stopped or it encounters an error.
	BootstrapAndCDC(ctx context.Context) error
	// Stop any ongoing CDC operations and shut down.
	Close(ctx context.Context) error
}
