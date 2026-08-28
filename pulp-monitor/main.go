//go:build wasip1

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/BananaLabs-OSS/Fiber/pulp"
	"github.com/vmihailenco/msgpack/v5"
)

func main() {}
func init() { pulp.OnInit(bootstrap) }

func bootstrap(_ []byte) error {
	store, err := newSQLiteEventStore("")
	if err != nil {
		return fmt.Errorf("open monitor SQLite store: %w", err)
	}
	cell, err := openOwner(context.Background(), store)
	if err != nil {
		return err
	}
	pulp.Provide(FnCatalog, func(_ []byte) ([]byte, error) {
		return msgpack.Marshal(Catalog{Version: ContractVersion, Commands: []string{
			string(UpsertComponent), string(EditComponent), string(ArchiveComponent), string(RestoreComponent),
			string(UpsertSource), string(EditSource), string(RevokeSource), string(RestoreSource),
			string(MapSourceTarget), string(UnmapSourceTarget),
			string(AppendObservation), string(IngestObservation), string(SweepReconcile),
			string(OpenIncident), string(EditIncident), string(UpdateIncident), string(ResolveIncident), string(DeleteIncident),
			string(ScheduleMaintenance), string(EditMaintenance), string(CancelMaintenance), string(DeleteMaintenance),
		}, Queries: []string{"component", "source", "incident", "maintenance", "all"}})
	})
	pulp.Provide(FnCommand, func(raw []byte) ([]byte, error) {
		var command Command
		if err := msgpack.Unmarshal(raw, &command); err != nil {
			return nil, fmt.Errorf("decode monitor command: %w", err)
		}
		if command.AtUnix == 0 {
			command.AtUnix = time.Now().Unix()
		}
		result, err := cell.apply(context.Background(), command)
		if err != nil {
			return nil, err
		}
		return msgpack.Marshal(result)
	})
	pulp.Provide(FnQuery, func(raw []byte) ([]byte, error) {
		var query Query
		if err := msgpack.Unmarshal(raw, &query); err != nil {
			return nil, fmt.Errorf("decode monitor query: %w", err)
		}
		if query.AtUnix == 0 {
			query.AtUnix = time.Now().Unix()
		}
		projection, err := cell.query(query)
		if err != nil {
			return nil, err
		}
		return msgpack.Marshal(projection)
	})
	pulp.Provide(FnProjection, func(_ []byte) ([]byte, error) { return msgpack.Marshal(cell.projection(time.Now().Unix())) })
	return nil
}
