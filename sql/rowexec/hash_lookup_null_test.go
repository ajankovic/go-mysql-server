// Copyright 2026 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rowexec

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/types"
)

// A hash join on = finds no candidate rows for a probe whose key is NULL, so a
// join of two sides with many NULL keys costs their rows, not the product of
// their NULL rows. A lookup that is not null-rejecting still keeps NULL keys
// and finds them.
func TestHashLookupProbeByNullKeyFindsNothingWhenNullRejecting(t *testing.T) {
	db := memory.NewDatabase("test")
	pro := memory.NewDBProvider(db)
	ctx := newContext(pro)
	sch := sql.NewPrimaryKeySchema(sql.Schema{{Name: "k", Source: "t", Type: types.Int64, Nullable: true}})
	tbl := memory.NewTable(ctx, db.BaseDatabase, "t", sch, nil)
	for _, k := range []any{int64(1), nil, nil, nil, int64(2)} {
		require.NoError(t, tbl.Insert(ctx, sql.NewRow(k)))
	}

	for _, tc := range []struct {
		name          string
		nullRejecting bool
		nullMatches   int
	}{
		{"null-rejecting", true, 0},
		{"null-safe", false, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := expression.Tuple{expression.NewGetField(0, types.Int64, "k", true)}
			hl := plan.NewHashLookup(ctx, plan.NewResolvedTable(tbl, nil, nil), key, key, plan.JoinTypeHash)
			hl.NullRejecting = tc.nullRejecting

			// The first probe builds the lookup from every row.
			first, err := DefaultBuilder.Build(ctx, hl, sql.NewRow(int64(1)))
			require.NoError(t, err)
			all, err := sql.RowIterToRows(ctx, first)
			require.NoError(t, err)
			require.Len(t, all, 5)

			probe := func(k any) int {
				iter, err := DefaultBuilder.Build(ctx, hl, sql.NewRow(k))
				require.NoError(t, err)
				rows, err := sql.RowIterToRows(ctx, iter)
				require.NoError(t, err)
				return len(rows)
			}
			require.Equal(t, tc.nullMatches, probe(nil))
			require.Equal(t, 1, probe(int64(2)))
		})
	}
}
