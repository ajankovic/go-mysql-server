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
	"fmt"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/types"
)

func TestGreatest(t *testing.T) {
	testCases := []struct {
		name     string
		args     []sql.Expression
		expected interface{}
	}{
		{
			"null",
			[]sql.Expression{
				expression.NewLiteral(nil, types.Null),
				expression.NewLiteral(5, types.Int64),
				expression.NewLiteral(1, types.Int64),
			},
			nil,
		},
		{
			"negative and all ints",
			[]sql.Expression{
				expression.NewLiteral(int64(-1), types.Int64),
				expression.NewLiteral(int64(5), types.Int64),
				expression.NewLiteral(int64(1), types.Int64),
			},
			int64(5),
		},
		{
			"string mixed",
			[]sql.Expression{
				expression.NewLiteral(string("9"), types.LongText),
				expression.NewLiteral(int64(5), types.Int64),
				expression.NewLiteral(int64(1), types.Int64),
			},
			float64(9),
		},
		{
			"unconvertible string mixed ignored",
			[]sql.Expression{
				expression.NewLiteral(string("10.5"), types.LongText),
				expression.NewLiteral(string("foobar"), types.Int64),
				expression.NewLiteral(int64(5), types.Int64),
				expression.NewLiteral(int64(1), types.Int64),
			},
			float64(10.5),
		},
		{
			"float mixed",
			[]sql.Expression{
				expression.NewLiteral(float64(10.0), types.Float64),
				expression.NewLiteral(int(5), types.Int64),
				expression.NewLiteral(int(1), types.Int64),
			},
			float64(10.0),
		},
		{
			"all strings",
			[]sql.Expression{
				expression.NewLiteral("aaa", types.LongText),
				expression.NewLiteral("bbb", types.LongText),
				expression.NewLiteral("9999", types.LongText),
				expression.NewLiteral("", types.LongText),
			},
			"bbb",
		},
		{
			"all strings and empty",
			[]sql.Expression{
				expression.NewLiteral("aaa", types.LongText),
				expression.NewLiteral("bbb", types.LongText),
				expression.NewLiteral("9999", types.LongText),
				expression.NewLiteral("", types.LongText),
			},
			"bbb",
		},
		{
			"nulls of a non-null type, char",
			[]sql.Expression{
				expression.NewConvert(expression.NewLiteral("aaa", types.LongText), types.LongText, expression.ConvertToChar),
				expression.NewConvert(expression.NewLiteral(nil, types.Null), types.LongText, expression.ConvertToChar),
			},
			nil,
		},
		{
			"nulls of a non-null type, signed",
			[]sql.Expression{
				expression.NewConvert(expression.NewLiteral(3.14159265359, types.Float64), types.Int64, expression.ConvertToSigned),
				expression.NewConvert(expression.NewLiteral(nil, types.Null), types.Int64, expression.ConvertToSigned),
			},
			nil,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			ctx := sql.NewEmptyContext()
			f, err := NewGreatest(sql.NewEmptyContext(), tt.args...)
			require.NoError(err)

			output, err := f.Eval(ctx, nil)
			require.NoError(err)
			require.Equal(tt.expected, output)
		})
	}
}

func TestGreatestLeastExactDecimalDirection(t *testing.T) {
	ctx := sql.NewEmptyContext()
	dt := types.MustCreateDecimalType(20, 19)
	low, err := types.InternalDecimalType.ConvertToDecimal("2.0000000000000000001")
	require.NoError(t, err)
	high, err := types.InternalDecimalType.ConvertToDecimal("2.0000000000000000002")
	require.NoError(t, err)

	for _, test := range []struct {
		name        string
		constructor func(*sql.Context, ...sql.Expression) (sql.Expression, error)
		expected    string
	}{
		{
			name:        "greatest",
			constructor: NewGreatest,
			expected:    high.String(),
		},
		{
			name:        "least",
			constructor: NewLeast,
			expected:    low.String(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := []sql.Expression{expression.NewLiteral(low, dt), expression.NewLiteral(high, dt)}
			for range 2 {
				f, err := test.constructor(ctx, args...)
				require.NoError(t, err)
				value, err := f.Eval(ctx, nil)
				require.NoError(t, err)
				decimal, err := types.InternalDecimalType.ConvertToDecimal(value)
				require.NoError(t, err)
				require.Equal(t, test.expected, decimal.String())

				args[0], args[1] = args[1], args[0]
			}
		})
	}
}

func TestGreatestUnsignedOverflow(t *testing.T) {
	require := require.New(t)
	ctx := sql.NewEmptyContext()

	var x int
	var gr sql.Expression
	var err error

	switch unsafe.Sizeof(x) {
	case 4:
		gr, err = NewGreatest(ctx, expression.NewLiteral(int32(1), types.Int32),
			expression.NewLiteral(uint32(4294967295), types.Uint32),
		)
		require.NoError(err)
	case 8:
		gr, err = NewGreatest(ctx, expression.NewLiteral(int64(1), types.Int64),
			expression.NewLiteral(uint64(18446744073709551615), types.Uint64),
		)
		require.NoError(err)
	default:
		// non 32/64 bits??
		return
	}

	_, err = gr.Eval(ctx, nil)
	require.EqualError(err, "Unsigned integer too big to fit on signed integer")
}

func TestLeast(t *testing.T) {
	testCases := []struct {
		name     string
		args     []sql.Expression
		expected interface{}
	}{
		{
			"null",
			[]sql.Expression{
				expression.NewLiteral(nil, types.Null),
				expression.NewLiteral(5, types.Int64),
				expression.NewLiteral(1, types.Int64),
			},
			nil,
		},
		{
			"negative and all ints",
			[]sql.Expression{
				expression.NewLiteral(int64(-1), types.Int64),
				expression.NewLiteral(int64(5), types.Int64),
				expression.NewLiteral(int64(1), types.Int64),
			},
			int64(-1),
		},
		{
			"string mixed",
			[]sql.Expression{
				expression.NewLiteral(string("10"), types.LongText),
				expression.NewLiteral(int64(5), types.Int64),
				expression.NewLiteral(int64(1), types.Int64),
			},
			float64(1),
		},
		{
			"unconvertible string mixed ignored",
			[]sql.Expression{
				expression.NewLiteral(string("10.5"), types.LongText),
				expression.NewLiteral(string("foobar"), types.Int64),
				expression.NewLiteral(int64(5), types.Int64),
				expression.NewLiteral(int64(1), types.Int64),
			},
			float64(1),
		},
		{
			"float mixed",
			[]sql.Expression{
				expression.NewLiteral(float64(10.0), types.Float64),
				expression.NewLiteral(int(5), types.Int64),
				expression.NewLiteral(int(1), types.Int64),
			},
			float64(1.0),
		},
		{
			"all strings",
			[]sql.Expression{
				expression.NewLiteral("aaa", types.LongText),
				expression.NewLiteral("bbb", types.LongText),
				expression.NewLiteral("9999", types.LongText),
			},
			"9999",
		},
		{
			"all strings and empty",
			[]sql.Expression{
				expression.NewLiteral("aaa", types.LongText),
				expression.NewLiteral("bbb", types.LongText),
				expression.NewLiteral("9999", types.LongText),
				expression.NewLiteral("", types.LongText),
			},
			"",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := sql.NewEmptyContext()
			require := require.New(t)

			f, err := NewLeast(ctx, tt.args...)
			require.NoError(err)

			output, err := f.Eval(ctx, nil)
			require.NoError(err)
			require.Equal(tt.expected, output)
		})
	}
}

// TestGreatestLeastTemporal checks that a time mixed with text or numbers
// compares as a time, in the type the time arguments merge to.
func TestGreatestLeastTemporal(t *testing.T) {
	ctx := sql.NewEmptyContext()
	qt := ctx.QueryTime()
	today := time.Date(qt.Year(), qt.Month(), qt.Day(), 0, 0, 0, 0, time.UTC)

	at := func(s string) time.Time {
		v, err := time.Parse("2006-01-02 15:04:05", s)
		require.NoError(t, err)
		return v
	}
	datetime := func(s string) sql.Expression {
		return expression.NewLiteral(at(s), types.DatetimeMaxPrecision)
	}
	date := func(s string) sql.Expression {
		return expression.NewLiteral(at(s+" 00:00:00"), types.Date)
	}
	timestamp := func(s string) sql.Expression {
		return expression.NewLiteral(at(s), types.Timestamp)
	}
	text := func(s string) sql.Expression {
		return expression.NewLiteral(s, types.LongText)
	}
	decimal := func(s string) sql.Expression {
		d, err := types.InternalDecimalType.ConvertToDecimal(s)
		require.NoError(t, err)
		return expression.NewLiteral(d, types.MustCreateDecimalType(14, 0))
	}
	timestampOf := func(s string) sql.Expression {
		f, err := NewDatetime(ctx, text(s))
		require.NoError(t, err)
		return f
	}
	timestampOfInt := func(n int64) sql.Expression {
		f, err := NewDatetime(ctx, expression.NewLiteral(n, types.Int64))
		require.NoError(t, err)
		return f
	}

	testCases := []struct {
		name            string
		args            []sql.Expression
		typ             sql.Type
		greatest, least interface{}
	}{
		{
			"datetime and string",
			[]sql.Expression{datetime("2026-03-15 06:00:00"), text("2026-04-01")},
			types.DatetimeMaxPrecision, at("2026-04-01 00:00:00"), at("2026-03-15 06:00:00"),
		},
		{
			"string and datetime",
			[]sql.Expression{text("2026-04-01"), datetime("2026-03-15 06:00:00")},
			types.DatetimeMaxPrecision, at("2026-04-01 00:00:00"), at("2026-03-15 06:00:00"),
		},
		{
			"datetime and TIMESTAMP()",
			[]sql.Expression{datetime("2026-03-15 06:00:00"), timestampOf("2026-04-01")},
			types.DatetimeMaxPrecision, at("2026-04-01 00:00:00"), at("2026-03-15 06:00:00"),
		},
		{
			"two TIMESTAMP()s",
			[]sql.Expression{timestampOf("2026-04-01"), timestampOf("2026-05-01")},
			types.DatetimeMaxPrecision, at("2026-05-01 00:00:00"), at("2026-04-01 00:00:00"),
		},
		{
			"datetime and CURDATE()",
			[]sql.Expression{datetime("1999-01-01 12:00:00"), NewCurrDate(ctx)},
			types.DatetimeMaxPrecision, today, at("1999-01-01 12:00:00"),
		},
		{
			"date and string",
			[]sql.Expression{date("2024-01-10"), text("2025-01-01")},
			types.Date, at("2025-01-01 00:00:00"), at("2024-01-10 00:00:00"),
		},
		{
			"date and a string with a time of day",
			[]sql.Expression{date("2024-01-10"), text("2025-01-01 10:00:00")},
			types.Date, at("2025-01-01 00:00:00"), at("2024-01-10 00:00:00"),
		},
		{
			"date and TIMESTAMP()",
			[]sql.Expression{date("2024-01-10"), timestampOf("2025-01-01")},
			types.DatetimeMaxPrecision, at("2025-01-01 00:00:00"), at("2024-01-10 00:00:00"),
		},
		{
			"timestamps",
			[]sql.Expression{timestamp("2026-08-20 18:00:00"), timestamp("2026-03-15 06:00:00")},
			types.Timestamp, at("2026-08-20 18:00:00"), at("2026-03-15 06:00:00"),
		},
		{
			"timestamp and a string outside the TIMESTAMP range",
			[]sql.Expression{timestamp("2026-08-20 18:00:00"), text("2050-01-01")},
			types.DatetimeMaxPrecision, at("2050-01-01 00:00:00"), at("2026-08-20 18:00:00"),
		},
		{
			"datetime and a string with fractional seconds",
			[]sql.Expression{expression.NewLiteral(at("2026-03-15 06:00:00"), types.Datetime), text("2026-03-15 06:00:00.6")},
			types.DatetimeMaxPrecision, at("2026-03-15 06:00:00.6"), at("2026-03-15 06:00:00"),
		},
		{
			"date and datetime",
			[]sql.Expression{date("2024-01-10"), datetime("2024-01-10 08:00:00")},
			types.DatetimeMaxPrecision, at("2024-01-10 08:00:00"), at("2024-01-10 00:00:00"),
		},
		{
			"dates",
			[]sql.Expression{date("2027-01-10"), date("2024-01-10")},
			types.Date, at("2027-01-10 00:00:00"), at("2024-01-10 00:00:00"),
		},
		{
			"datetime and an integer",
			[]sql.Expression{datetime("2026-03-15 06:00:00"), expression.NewLiteral(int64(20260401), types.Int64)},
			types.DatetimeMaxPrecision, at("2026-04-01 00:00:00"), at("2026-03-15 06:00:00"),
		},
		{
			"date and an integer",
			[]sql.Expression{date("2026-03-15"), expression.NewLiteral(int32(20260401), types.Int32)},
			types.Date, at("2026-04-01 00:00:00"), at("2026-03-15 00:00:00"),
		},
		{
			"TIMESTAMP() of numbers",
			[]sql.Expression{timestampOfInt(20260401), timestampOfInt(20260501)},
			types.DatetimeMaxPrecision, at("2026-05-01 00:00:00"), at("2026-04-01 00:00:00"),
		},
		{
			"datetime and a decimal",
			[]sql.Expression{datetime("2026-03-15 06:00:00"), decimal("20260401120000")},
			types.DatetimeMaxPrecision, at("2026-04-01 12:00:00"), at("2026-03-15 06:00:00"),
		},
		{
			"datetime and a NULL string",
			[]sql.Expression{datetime("2026-03-15 06:00:00"), expression.NewLiteral(nil, types.LongText)},
			types.DatetimeMaxPrecision, nil, nil,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			g, err := NewGreatest(ctx, tt.args...)
			require.NoError(err)
			require.Equal(tt.typ, g.Type(ctx))
			output, err := g.Eval(ctx, nil)
			require.NoError(err)
			require.Equal(tt.greatest, output)

			l, err := NewLeast(ctx, tt.args...)
			require.NoError(err)
			require.Equal(tt.typ, l.Type(ctx))
			output, err = l.Eval(ctx, nil)
			require.NoError(err)
			require.Equal(tt.least, output)
		})
	}

	for _, arg := range []sql.Expression{expression.NewLiteral(int64(99999999), types.Int64), text("not a date")} {
		t.Run(fmt.Sprintf("%v is left out with a warning", arg), func(t *testing.T) {
			for _, newF := range []func(*sql.Context, ...sql.Expression) (sql.Expression, error){NewGreatest, NewLeast} {
				ctx := sql.NewEmptyContext()
				f, err := newF(ctx, datetime("2026-03-15 06:00:00"), arg)
				require.NoError(t, err)
				output, err := f.Eval(ctx, nil)
				require.NoError(t, err)
				require.Equal(t, at("2026-03-15 06:00:00"), output)
				require.Len(t, ctx.Warnings(), 1)
			}
		})
	}

	t.Run("no argument names a time", func(t *testing.T) {
		for _, newF := range []func(*sql.Context, ...sql.Expression) (sql.Expression, error){NewGreatest, NewLeast} {
			ctx := sql.NewEmptyContext()
			f, err := newF(ctx, expression.NewLiteral("not a date", types.DatetimeMaxPrecision), text("not a date either"))
			require.NoError(t, err)
			output, err := f.Eval(ctx, nil)
			require.NoError(t, err)
			require.Nil(t, output)
			require.Len(t, ctx.Warnings(), 2)
		}
	})
}
