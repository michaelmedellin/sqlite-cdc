// SPDX-FileCopyrightText: © 2024 Kevin Conway <kevin@conway0.com>
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	cdc "github.com/michaelmedellin/sqlite-cdc"
)

// HTTPBasicPOST implements the cdc.ChangesHandler interface by making POST
// requests to an HTTP endpoint.
//
// Targets of this handler will receive HTTP POST requests with the following
// format:
//
//	{
//	  "changes": [
//	    {
//	      "table": "table_name",
//	      "timestamp": "2022-01-01T00:00:00Z",
//	      "operation": "INSERT", // or "UPDATE" or "DELETE"
//	      "before": { // present for updates and deletes
//	        "key": "value",
//	        ...
//	      },
//	      "after": { // present for inserts and updates
//	        "key": "value",
//	        ...
//	      }
//	    },
//	    ...
//	  ]
//	}
type HTTPBasicPOST struct {
	Client   *http.Client
	Endpoint string
}

func (h *HTTPBasicPOST) HandleChanges(ctx context.Context, changes cdc.Changes) error {
	cs := jsonChanges{Changes: changes}
	b, err := json.Marshal(cs)
	if err != nil {
		return fmt.Errorf("%w: failed to marshal changes for POST", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.Endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%w: failed to create POST request", err)
	}
	resp, err := h.Client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("%w: failed to POST changes", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("%w: failed to read error response body", err)
		}
		return fmt.Errorf("%w: HTTP status %d: %s", err, resp.StatusCode, string(b))
	}
	return nil
}

type jsonChanges struct {
	Changes cdc.Changes `json:"changes"`
}
