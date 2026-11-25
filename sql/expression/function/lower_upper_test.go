// Copyright 2020-2021 Dolthub, Inc.
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

package function

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/types"
)

func TestLower(t *testing.T) {
	testCases := []struct {
		name     string
		rowType  sql.Type
		row      sql.Row
		expected interface{}
		typ      sql.Type
	}{
		{"text nil", types.LongText, sql.NewRow(nil), nil, types.LongText},
		{"text ok", types.LongText, sql.NewRow("LoWeR"), "lower", types.LongText},
		{"binary ok", types.Blob, sql.NewRow([]byte("LoWeR")), "LoWeR", types.Blob},
		{"other type", types.Int32, sql.NewRow(int32(1)), "1", types.LongText},
	}

	for _, tt := range testCases {
		ctx := sql.NewEmptyContext()
		f := NewLower(ctx, expression.NewGetField(0, tt.rowType, "", true))

		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, eval(t, f, tt.row))
		})

		req := require.New(t)
		req.True(f.IsNullable(ctx))
		req.Equal(tt.typ, f.Type(ctx))
	}
}

func TestUpper(t *testing.T) {
	testCases := []struct {
		name     string
		rowType  sql.Type
		row      sql.Row
		expected interface{}
		typ      sql.Type
	}{
		{"text nil", types.LongText, sql.NewRow(nil), nil, types.LongText},
		{"text ok", types.LongText, sql.NewRow("UpPeR"), "UPPER", types.LongText},
		{"binary ok", types.Blob, sql.NewRow([]byte("UpPeR")), "UpPeR", types.Blob},
		{"other type", types.Int32, sql.NewRow(int32(1)), "1", types.LongText},
	}

	for _, tt := range testCases {
		ctx := sql.NewEmptyContext()
		f := NewUpper(ctx, expression.NewGetField(0, tt.rowType, "", true))

		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, eval(t, f, tt.row))
		})

		req := require.New(t)
		req.True(f.IsNullable(ctx))
		req.Equal(tt.typ, f.Type(ctx))
	}
}
