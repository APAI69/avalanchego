// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package state

import (
	"fmt"
	"strings"

	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/snow/choices"
	"github.com/ava-labs/gecko/snow/consensus/avalanche"
	"github.com/ava-labs/gecko/snow/consensus/snowstorm"
	"github.com/ava-labs/gecko/utils/formatting"
)

// uniqueVertex acts as a cache for vertices in the database.
//
// If a vertex is loaded, it will have one canonical uniqueVertex. The vertex
// will eventually be evicted from memory, when the uniqueVertex is evicted from
// the cache. If the uniqueVertex has a function called again afther this
// eviction, the vertex will be re-loaded from the database.
type uniqueVertex struct {
	serializer *Serializer

	vtxID ids.ID
	bytes []byte
	v     *vertexState
}

func (vtx *uniqueVertex) refresh() {
	parsed := false
	parseErrored := false
	// Prevent segfault
	if vtx.v == nil {
		vtx.v = &vertexState{}
	}
	if !vtx.v.unique {
		fmt.Printf("vertex not unique checking de-duplicator\n")
		unique := vtx.serializer.state.UniqueVertex(vtx)
		prevVtx := vtx.v.vtx
		// If de-duplicator did not return a different value from cache
		// Then get status from db and set unique to true
		if unique == vtx {
			fmt.Printf("Nobody was in the cache, checking for my own previous status\n")
			vtx.v.status = vtx.serializer.state.Status(vtx.ID())
			fmt.Printf("status was: %s \n", vtx.v.status.String())
			vtx.v.unique = true
		} else {
			// If someone is in the cache, they must be up to date
			fmt.Printf("somebody else was in the cache, setting ourselves to be equal\n")
			bytes := vtx.bytes
			*vtx = *unique // TODO can we switch this to point to the same vertexState???
			vtx.bytes = bytes
		}

		switch {
		case vtx.v.vtx == nil && prevVtx == nil && vtx.bytes != nil:
			fmt.Printf("parsing unique vertex\n")
			parsed = true
			if parsedVtx, err := vtx.serializer.parseVertex(vtx.bytes); err != nil {
				vtx.v.vtx = parsedVtx
				vtx.storeAndUpdateStatus()
			} else {
				parseErrored = true
				fmt.Printf("error while parsing vertex in unique tx\n")
				vtx.v.vtx = &vertex{}
				vtx.v.validity = err
				vtx.v.verified = true
			}
		case vtx.v.vtx == nil && prevVtx == nil:
			fmt.Printf("fetching unique vertex from db\n")
			vtx.v.vtx = vtx.serializer.state.Vertex(vtx.ID())
		case vtx.v.vtx == nil:
			fmt.Printf("setting to prevVtx\n")
			if prevVtx == nil {
				fmt.Printf("prevVtx was also nil\n")
			}
			vtx.v.vtx = prevVtx
		}
	}
	if vtx.v.vtx == nil {
		fmt.Printf("interior vtx was nil after refresh \n")
		fmt.Printf("Parsed: %v \n", parsed)
		fmt.Printf("Parse Errored: %v \n", parseErrored)
	}
	fmt.Printf("Finished refresh\n")
}

func (vtx *uniqueVertex) Evict() {
	if vtx.v != nil {
		vtx.v.unique = false
	}
}

func (vtx *uniqueVertex) setVertex(innerVtx *vertex) {
	vtx.refresh()
	if vtx.v.vtx == nil {
		vtx.v.vtx = innerVtx
		vtx.serializer.state.SetVertex(innerVtx)
		vtx.setStatus(choices.Processing)
	}
}

// Assumes vtx.v.vtx != nil
func (vtx *uniqueVertex) storeAndUpdateStatus() {
	vtx.serializer.state.SetVertex(vtx.v.vtx)
	if status := vtx.serializer.state.Status(vtx.ID()); status == choices.Unknown {
		vtx.serializer.state.SetStatus(vtx.ID(), choices.Processing)
		vtx.v.status = choices.Processing
	} else {
		vtx.v.status = status
	}
}

func (vtx *uniqueVertex) setStatus(status choices.Status) {
	vtx.refresh()
	if vtx.v.status != status {
		vtx.serializer.state.SetStatus(vtx.ID(), status)
		vtx.v.status = status
	}
}

func (vtx *uniqueVertex) ID() ids.ID { return vtx.vtxID }

func (vtx *uniqueVertex) Accept() error {
	vtx.setStatus(choices.Accepted)

	vtx.serializer.edge.Add(vtx.vtxID)
	for _, parent := range vtx.Parents() {
		vtx.serializer.edge.Remove(parent.ID())
	}

	vtx.serializer.state.SetEdge(vtx.serializer.edge.List())

	// Should never traverse into parents of a decided vertex. Allows for the
	// parents to be garbage collected
	vtx.v.parents = nil

	return vtx.serializer.db.Commit()
}

func (vtx *uniqueVertex) Reject() error {
	vtx.setStatus(choices.Rejected)

	// Should never traverse into parents of a decided vertex. Allows for the
	// parents to be garbage collected
	vtx.v.parents = nil

	return vtx.serializer.db.Commit()
}

func (vtx *uniqueVertex) Status() choices.Status { vtx.refresh(); return vtx.v.status }

func (vtx *uniqueVertex) Parents() []avalanche.Vertex {
	vtx.refresh()

	if len(vtx.v.parents) != len(vtx.v.vtx.parentIDs) {
		vtx.v.parents = make([]avalanche.Vertex, len(vtx.v.vtx.parentIDs))
		for i, parentID := range vtx.v.vtx.parentIDs {
			vtx.v.parents[i] = &uniqueVertex{
				serializer: vtx.serializer,
				vtxID:      parentID,
			}
		}
	}

	return vtx.v.parents
}

func (vtx *uniqueVertex) Height() uint64 {
	vtx.refresh()

	return vtx.v.vtx.height
}

func (vtx *uniqueVertex) Txs() []snowstorm.Tx {
	vtx.refresh()

	if len(vtx.v.vtx.txs) != len(vtx.v.txs) {
		vtx.v.txs = make([]snowstorm.Tx, len(vtx.v.vtx.txs))
		for i, tx := range vtx.v.vtx.txs {
			vtx.v.txs[i] = tx
		}
	}

	return vtx.v.txs
}

func (vtx *uniqueVertex) Bytes() []byte {
	if vtx.bytes != nil {
		return vtx.bytes
	} else {
		return vtx.v.vtx.Bytes()
	}
}

func (vtx *uniqueVertex) Verify() error {
	if vtx.v.verified {
		return vtx.v.validity
	} else {
		vtx.v.validity = vtx.v.vtx.Verify()
		vtx.v.verified = true
		return vtx.v.validity
	}
}

func (vtx *uniqueVertex) String() string {
	sb := strings.Builder{}

	parents := vtx.Parents()
	txs := vtx.Txs()

	sb.WriteString(fmt.Sprintf(
		"Vertex(ID = %s, Status = %s, Number of Dependencies = %d, Number of Transactions = %d)",
		vtx.ID(),
		vtx.Status(),
		len(parents),
		len(txs),
	))

	parentFormat := fmt.Sprintf("\n    Parent[%s]: ID = %%s, Status = %%s",
		formatting.IntFormat(len(parents)-1))
	for i, parent := range parents {
		sb.WriteString(fmt.Sprintf(parentFormat, i, parent.ID(), parent.Status()))
	}

	txFormat := fmt.Sprintf("\n    Transaction[%s]: ID = %%s, Status = %%s",
		formatting.IntFormat(len(txs)-1))
	for i, tx := range txs {
		sb.WriteString(fmt.Sprintf(txFormat, i, tx.ID(), tx.Status()))
	}

	return sb.String()
}

type vertexState struct {
	unique, verified bool

	validity error
	vtx      *vertex
	status   choices.Status

	parents []avalanche.Vertex
	txs     []snowstorm.Tx
}
