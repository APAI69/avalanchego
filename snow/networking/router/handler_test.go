// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package router

import (
	"errors"
	"testing"
	"time"

	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/snow"
	"github.com/ava-labs/gecko/snow/engine/common"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHandlerCallsToClose(t *testing.T) {
	engine := common.EngineTest{T: t}
	engine.Default(false)

	closed := make(chan struct{}, 1)

	engine.ContextF = snow.DefaultContextTest
	engine.GetAcceptedFrontierF = func(validatorID ids.ShortID, requestID uint32) error {
		return errors.New("Engine error should cause handler to close")
	}

	handler := &Handler{}
	handler.Initialize(
		&engine,
		nil,
		1,
		"",
		prometheus.NewRegistry(),
	)

	handler.toClose = func() {
		closed <- struct{}{}
	}
	go handler.Dispatch()

	handler.GetAcceptedFrontier(ids.NewShortID([20]byte{}), 1)

	ticker := time.NewTicker(20 * time.Millisecond)
	select {
	case _, _ = <-ticker.C:
		t.Fatalf("Handler shutdown timed out before calling toClose")
	case _, _ = <-closed:
	}
}
