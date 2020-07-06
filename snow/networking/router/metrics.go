// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package router

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ava-labs/gecko/utils/timer"
	"github.com/ava-labs/gecko/utils/wrappers"
)

func initHistogram(namespace, name string, registerer prometheus.Registerer, errs *wrappers.Errs) prometheus.Histogram {
	histogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      name,
			Help:      "Time spent processing this request in nanoseconds",
			Buckets:   timer.NanosecondsBuckets,
		})

	if err := registerer.Register(histogram); err != nil {
		errs.Add(fmt.Errorf("failed to register %s statistics due to %s", name, err))
	}
	return histogram
}

type metrics struct {
	registerer prometheus.Registerer
	pending    prometheus.Gauge
	dropped    prometheus.Counter
	getAcceptedFrontier, acceptedFrontier, getAcceptedFrontierFailed,
	getAccepted, accepted, getAcceptedFailed,
	getAncestors, multiPut, getAncestorsFailed,
	get, put, getFailed,
	pushQuery, pullQuery, chits, queryFailed,
	notify,
	gossip,
	shutdown prometheus.Histogram
}

// Initialize implements the Engine interface
func (m *metrics) Initialize(namespace string, registerer prometheus.Registerer) error {
	m.registerer = registerer
	errs := wrappers.Errs{}

	m.pending = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "pending",
			Help:      "Number of pending events",
		})

	if err := registerer.Register(m.pending); err != nil {
		errs.Add(fmt.Errorf("failed to register pending statistics due to %s", err))
	}

	m.dropped = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "dropped",
			Help:      "Number of dropped events",
		})

	if err := registerer.Register(m.dropped); err != nil {
		errs.Add(fmt.Errorf("failed to register dropped statistics due to %s", err))
	}

	m.getAcceptedFrontier = initHistogram(namespace, "get_accepted_frontier", registerer, &errs)
	m.acceptedFrontier = initHistogram(namespace, "accepted_frontier", registerer, &errs)
	m.getAcceptedFrontierFailed = initHistogram(namespace, "get_accepted_frontier_failed", registerer, &errs)
	m.getAccepted = initHistogram(namespace, "get_accepted", registerer, &errs)
	m.accepted = initHistogram(namespace, "accepted", registerer, &errs)
	m.getAcceptedFailed = initHistogram(namespace, "get_accepted_failed", registerer, &errs)
	m.getAncestors = initHistogram(namespace, "get_ancestors", registerer, &errs)
	m.multiPut = initHistogram(namespace, "multi_put", registerer, &errs)
	m.getAncestorsFailed = initHistogram(namespace, "get_ancestors_failed", registerer, &errs)
	m.get = initHistogram(namespace, "get", registerer, &errs)
	m.put = initHistogram(namespace, "put", registerer, &errs)
	m.getFailed = initHistogram(namespace, "get_failed", registerer, &errs)
	m.pushQuery = initHistogram(namespace, "push_query", registerer, &errs)
	m.pullQuery = initHistogram(namespace, "pull_query", registerer, &errs)
	m.chits = initHistogram(namespace, "chits", registerer, &errs)
	m.queryFailed = initHistogram(namespace, "query_failed", registerer, &errs)
	m.notify = initHistogram(namespace, "notify", registerer, &errs)
	m.gossip = initHistogram(namespace, "gossip", registerer, &errs)
	m.shutdown = initHistogram(namespace, "shutdown", registerer, &errs)

	return errs.Err
}

// Shutdown unregisters metrics
// must be called after the last metric has been used
func (m *metrics) Shutdown() error {
	if m.registerer == nil {
		return nil
	}

	if m.registerer.Unregister(m.pending) &&
		m.registerer.Unregister(m.dropped) &&
		m.registerer.Unregister(m.getAcceptedFrontier) &&
		m.registerer.Unregister(m.acceptedFrontier) &&
		m.registerer.Unregister(m.getAccepted) &&
		m.registerer.Unregister(m.accepted) &&
		m.registerer.Unregister(m.getAcceptedFailed) &&
		m.registerer.Unregister(m.getAncestors) &&
		m.registerer.Unregister(m.multiPut) &&
		m.registerer.Unregister(m.getAncestorsFailed) &&
		m.registerer.Unregister(m.get) &&
		m.registerer.Unregister(m.put) &&
		m.registerer.Unregister(m.getFailed) &&
		m.registerer.Unregister(m.pushQuery) &&
		m.registerer.Unregister(m.pullQuery) &&
		m.registerer.Unregister(m.chits) &&
		m.registerer.Unregister(m.queryFailed) &&
		m.registerer.Unregister(m.notify) &&
		m.registerer.Unregister(m.gossip) &&
		m.registerer.Unregister(m.shutdown) {
		return nil
	}

	return fmt.Errorf("could not unregister all chain handler metrics")
}
