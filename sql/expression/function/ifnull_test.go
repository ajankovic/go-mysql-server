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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/types"
)

func TestIfNull(t *testing.T) {
	date := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	datetime := time.Date(2026, 3, 15, 6, 0, 0, 123400000, time.UTC)
	testCases := []struct {
		expression     interface{}
		expressionType sql.Type
		value          interface{}
		valueType      sql.Type
		expected       interface{}
		expectedType   sql.Type
	}{
		{"foo", types.LongText, "bar", types.LongText, "foo", types.LongText},
		{"foo", types.LongText, "foo", types.LongText, "foo", types.LongText},
		{nil, types.LongText, "foo", types.LongText, "foo", types.LongText},
		{"foo", types.LongText, nil, types.LongText, "foo", types.LongText},
		{nil, types.LongText, nil, types.LongText, nil, types.LongText},
		{"", types.LongText, nil, types.LongText, "", types.LongText},
		{nil, types.Int8, 128, types.Int64, int64(128), types.Int64},
		{date, types.Date, "foo", types.LongText, "2026-08-20", types.LongText},
		{nil, types.LongText, date, types.Date, "2026-08-20", types.LongText},
		{datetime, types.DatetimeMaxPrecision, "foo", types.LongText, "2026-03-15 06:00:00.123400", types.LongText},
	}

	ctx := sql.NewEmptyContext()
	for _, tc := range testCases {
		f := NewIfNull(
			ctx,
			expression.NewGetField(0, tc.expressionType, "expression", true),
			expression.NewGetField(1, tc.valueType, "value", true),
		)
		require.Equal(t, tc.expectedType, f.Type(ctx))
		v, err := f.Eval(sql.NewEmptyContext(), sql.NewRow(tc.expression, tc.value))
		require.NoError(t, err)
		require.Equal(t, tc.expected, v)
	}
}
