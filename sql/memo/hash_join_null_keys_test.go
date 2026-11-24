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

package memo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	sqle "github.com/dolthub/go-mysql-server"
	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/transform"
	"github.com/dolthub/go-mysql-server/sql/types"
)

// A hash join whose every key is compared with = leaves NULL keys out of its
// lookup; one that tells a NULL comparison from a false one (NOT IN) keeps
// them. A join on <=> is not hashed, and if it ever is, it keeps them too.
func TestHashJoinRejectsNullKeysOnlyWhenEveryKeyIsEquals(t *testing.T) {
	db := memory.NewDatabase("mydb")
	pro := memory.NewDBProvider(db)
	ctx := sql.NewContext(context.Background(), sql.WithSession(memory.NewSession(sql.NewBaseSession(), pro)))
	ctx.SetCurrentDatabase("mydb")
	for _, name := range []string{"t1", "t2"} {
		sch := sql.NewPrimaryKeySchema(sql.Schema{
			{Name: "i", Source: name, Type: types.Int64, PrimaryKey: true},
			{Name: "k", Source: name, Type: types.Int64, Nullable: true},
		})
		db.AddTable(name, memory.NewTable(ctx, db.BaseDatabase, name, sch, nil))
	}
	e := sqle.NewDefault(pro)

	for _, tc := range []struct {
		query  string
		hashed bool
		want   bool
	}{
		{"select /*+ HASH_JOIN(t1, t2) */ * from t1 join t2 on t1.k = t2.k", true, true},
		{"select /*+ HASH_JOIN(t1, t2) */ * from t1 left join t2 on t1.k = t2.k", true, true},
		{"select /*+ HASH_JOIN(t1, t2) */ * from t1 join t2 on t1.k = t2.k and t1.i = t2.i", true, true},
		{"select /*+ HASH_JOIN(t2, t1) */ * from t2 where k not in (select k from t1)", true, false},
		{"select /*+ HASH_JOIN(t1, t2) */ * from t1 join t2 on t1.k <=> t2.k", false, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			n, err := e.AnalyzeQuery(ctx, tc.query)
			require.NoError(t, err)
			var lookups []*plan.HashLookup
			transform.Inspect(n, func(n sql.Node) bool {
				if hl, ok := n.(*plan.HashLookup); ok {
					lookups = append(lookups, hl)
				}
				return true
			})
			if tc.hashed {
				require.Len(t, lookups, 1, sql.DebugString(ctx, n))
			}
			for _, hl := range lookups {
				require.Equal(t, tc.want, hl.NullRejecting)
			}
		})
	}
}
