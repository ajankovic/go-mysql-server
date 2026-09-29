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

package aggregation

import (
	"errors"
	"io"
	"math/rand/v2"
	"testing"

	"github.com/dolthub/vitess/go/vt/proto/query"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/types"
)

// Aggregations that keep state from one frame to the next rely on every framer
// moving a frame's ends only forward within a partition. These tests drive each
// framer the engine builds, with dummyFrame's offsets (2 preceding, 1
// following) and RANGE frames ordered by column 0, over partitions of seeded
// random rows.

type namedFramer struct {
	name   string
	framer sql.WindowFramer
}

var frameConstructors = []struct {
	name string
	new  func(sql.WindowFrame, *sql.WindowDefinition) (sql.WindowFramer, error)
}{
	{"RowsUnboundedPrecedingToNPreceding", NewRowsUnboundedPrecedingToNPrecedingFramer},
	{"RowsUnboundedPrecedingToCurrentRow", NewRowsUnboundedPrecedingToCurrentRowFramer},
	{"RowsUnboundedPrecedingToNFollowing", NewRowsUnboundedPrecedingToNFollowingFramer},
	{"RowsUnboundedPrecedingToUnboundedFollowing", NewRowsUnboundedPrecedingToUnboundedFollowingFramer},
	{"RowsNPrecedingToNPreceding", NewRowsNPrecedingToNPrecedingFramer},
	{"RowsNPrecedingToCurrentRow", NewRowsNPrecedingToCurrentRowFramer},
	{"RowsNPrecedingToNFollowing", NewRowsNPrecedingToNFollowingFramer},
	{"RowsNPrecedingToUnboundedFollowing", NewRowsNPrecedingToUnboundedFollowingFramer},
	{"RowsCurrentRowToNPreceding", NewRowsCurrentRowToNPrecedingFramer},
	{"RowsCurrentRowToCurrentRow", NewRowsCurrentRowToCurrentRowFramer},
	{"RowsCurrentRowToNFollowing", NewRowsCurrentRowToNFollowingFramer},
	{"RowsCurrentRowToUnboundedFollowing", NewRowsCurrentRowToUnboundedFollowingFramer},
	{"RowsNFollowingToNPreceding", NewRowsNFollowingToNPrecedingFramer},
	{"RowsNFollowingToCurrentRow", NewRowsNFollowingToCurrentRowFramer},
	{"RowsNFollowingToNFollowing", NewRowsNFollowingToNFollowingFramer},
	{"RowsNFollowingToUnboundedFollowing", NewRowsNFollowingToUnboundedFollowingFramer},
	{"RangeUnboundedPrecedingToNPreceding", NewRangeUnboundedPrecedingToNPrecedingFramer},
	{"RangeUnboundedPrecedingToCurrentRow", NewRangeUnboundedPrecedingToCurrentRowFramer},
	{"RangeUnboundedPrecedingToNFollowing", NewRangeUnboundedPrecedingToNFollowingFramer},
	{"RangeUnboundedPrecedingToUnboundedFollowing", NewRangeUnboundedPrecedingToUnboundedFollowingFramer},
	{"RangeNPrecedingToNPreceding", NewRangeNPrecedingToNPrecedingFramer},
	{"RangeNPrecedingToCurrentRow", NewRangeNPrecedingToCurrentRowFramer},
	{"RangeNPrecedingToNFollowing", NewRangeNPrecedingToNFollowingFramer},
	{"RangeNPrecedingToUnboundedFollowing", NewRangeNPrecedingToUnboundedFollowingFramer},
	{"RangeCurrentRowToNPreceding", NewRangeCurrentRowToNPrecedingFramer},
	{"RangeCurrentRowToCurrentRow", NewRangeCurrentRowToCurrentRowFramer},
	{"RangeCurrentRowToNFollowing", NewRangeCurrentRowToNFollowingFramer},
	{"RangeCurrentRowToUnboundedFollowing", NewRangeCurrentRowToUnboundedFollowingFramer},
	{"RangeNFollowingToNPreceding", NewRangeNFollowingToNPrecedingFramer},
	{"RangeNFollowingToCurrentRow", NewRangeNFollowingToCurrentRowFramer},
	{"RangeNFollowingToNFollowing", NewRangeNFollowingToNFollowingFramer},
	{"RangeNFollowingToUnboundedFollowing", NewRangeNFollowingToUnboundedFollowingFramer},
}

// windowFramers returns every framer, ordered by orderBy where one orders.
func windowFramers(t testing.TB, orderBy sql.Expression) []namedFramer {
	w := &sql.WindowDefinition{OrderBy: sql.SortConditions{{Expr: orderBy, Order: sql.Ascending}}}
	framers := []namedFramer{
		{"Partition", NewPartitionFramer()},
		{"GroupBy", NewGroupByFramer()},
		{"PeerGroup", NewPeerGroupFramer([]sql.Expression{orderBy})},
	}
	for _, c := range frameConstructors {
		framer, err := c.new(dummyFrame{}, w)
		require.NoError(t, err)
		framers = append(framers, namedFramer{c.name, framer})
	}
	return framers
}

// framesOf returns the frames framer yields over each partition, in order.
func framesOf(t testing.TB, framer sql.WindowFramer, partitions []sql.WindowInterval, buf sql.WindowBuffer) [][]sql.WindowInterval {
	ctx := sql.NewEmptyContext()
	frames := make([][]sql.WindowInterval, len(partitions))
	for i, p := range partitions {
		f, err := framer.NewFramer(p)
		require.NoError(t, err)
		for {
			frame, err := f.Next(ctx, buf)
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)
			frames[i] = append(frames[i], frame)
		}
	}
	return frames
}

// frameTestPartitions are consecutive partitions of these sizes, the first empty.
func frameTestPartitions(sizes ...int) []sql.WindowInterval {
	partitions := []sql.WindowInterval{{}}
	start := 0
	for _, n := range sizes {
		partitions = append(partitions, sql.WindowInterval{Start: start, End: start + n})
		start += n
	}
	return partitions
}

var framePartitions = frameTestPartitions(1, 2, 7, 40, 200)

// frameBuffer fills framePartitions with rows of an ordering column that rises
// with repeats (RANGE frames and peer groups need it sorted, and NULL-free),
// an integer column with repeats and NULLs, and a case-insensitive string
// column in which 'a' and 'A' are equal and NULLs occur.
func frameBuffer() sql.WindowBuffer {
	rng := rand.New(rand.NewPCG(1, 2))
	strs := []string{"a", "A", "b", "B", "c", "C"}
	var buf sql.WindowBuffer
	for _, p := range framePartitions {
		ord := int64(0)
		for range p.End - p.Start {
			ord += rng.Int64N(3)
			var x, s interface{}
			if rng.IntN(5) > 0 {
				x = rng.Int64N(10)
			}
			if rng.IntN(5) > 0 {
				s = strs[rng.IntN(len(strs))]
			}
			buf = append(buf, sql.Row{ord, x, s})
		}
	}
	return buf
}

var frameOrderBy = expression.NewGetField(0, types.Int64, "ord", false)

func TestWindowFramersMoveFramesForward(t *testing.T) {
	buf := frameBuffer()
	for _, f := range windowFramers(t, frameOrderBy) {
		t.Run(f.name, func(t *testing.T) {
			for i, frames := range framesOf(t, f.framer, framePartitions, buf) {
				p := framePartitions[i]
				prev := sql.WindowInterval{Start: p.Start, End: p.Start}
				for _, frame := range frames {
					require.LessOrEqual(t, p.Start, frame.Start, "frame %v in partition %v", frame, p)
					require.LessOrEqual(t, frame.Start, frame.End, "frame %v in partition %v", frame, p)
					require.LessOrEqual(t, frame.End, p.End, "frame %v in partition %v", frame, p)
					require.LessOrEqual(t, prev.Start, frame.Start, "frame %v after %v", frame, prev)
					require.LessOrEqual(t, prev.End, frame.End, "frame %v after %v", frame, prev)
					prev = frame
				}
			}
		})
	}
}

// backwardFrames moves a frame backwards through each partition, which no
// framer does, so an aggregation must recompute rather than rely on its state.
func backwardFrames(partitions []sql.WindowInterval) [][]sql.WindowInterval {
	frames := make([][]sql.WindowInterval, len(partitions))
	for i, p := range partitions {
		for r := p.End - 1; r >= p.Start; r-- {
			frames[i] = append(frames[i], sql.WindowInterval{Start: max(p.Start, r-2), End: min(p.End, r+2)})
		}
	}
	return frames
}

// scanExtremum is MAX (keep 1) or MIN (keep -1) by definition: the first of
// the frame's non-NULL values that no other value in it beats.
func scanExtremum(t *testing.T, ctx *sql.Context, typ sql.Type, col, keep int, buf sql.WindowBuffer, frame sql.WindowInterval) interface{} {
	var best interface{}
	for _, row := range buf[frame.Start:frame.End] {
		v := row[col]
		if v == nil {
			continue
		}
		if best == nil {
			best = v
			continue
		}
		cmp, err := typ.Compare(ctx, v, best)
		require.NoError(t, err)
		if cmp == keep {
			best = v
		}
	}
	return best
}

func TestMinMaxAggMatchFrameScan(t *testing.T) {
	buf := frameBuffer()
	columns := []struct {
		name string
		col  int
		typ  sql.Type
	}{
		{"ints", 1, types.Int64},
		{"case-insensitive strings", 2, types.MustCreateString(query.Type_VARCHAR, 10, sql.Collation_utf8mb4_0900_ai_ci)},
	}
	aggs := []struct {
		name string
		keep int
		new  func(sql.Expression) sql.WindowFunction
	}{
		{"max", 1, func(e sql.Expression) sql.WindowFunction { return NewMaxAgg(e) }},
		{"min", -1, func(e sql.Expression) sql.WindowFunction { return NewMinAgg(e) }},
	}
	shapes := []struct {
		name   string
		frames [][]sql.WindowInterval
	}{{"backwards", backwardFrames(framePartitions)}}
	for _, f := range windowFramers(t, frameOrderBy) {
		shapes = append(shapes, struct {
			name   string
			frames [][]sql.WindowInterval
		}{f.name, framesOf(t, f.framer, framePartitions, buf)})
	}
	for _, agg := range aggs {
		for _, column := range columns {
			for _, shape := range shapes {
				t.Run(agg.name+"/"+column.name+"/"+shape.name, func(t *testing.T) {
					ctx := sql.NewEmptyContext()
					fn := agg.new(expression.NewGetField(column.col, column.typ, "x", true))
					for i, p := range framePartitions {
						require.NoError(t, fn.StartPartition(ctx, p, buf))
						for _, frame := range shape.frames[i] {
							actual, err := fn.Compute(ctx, frame, buf)
							require.NoError(t, err)
							require.Equal(t, scanExtremum(t, ctx, column.typ, column.col, agg.keep, buf, frame), actual, "frame %v", frame)
						}
					}
				})
			}
		}
	}
}

// spanningFramers are the framers whose frames grow with the partition, where
// rescanning each frame would cost one evaluation per row of every frame.
func spanningFramers(t testing.TB) []namedFramer {
	var framers []namedFramer
	for _, f := range windowFramers(t, frameOrderBy) {
		switch f.name {
		case "Partition", "RowsUnboundedPrecedingToCurrentRow", "RowsCurrentRowToUnboundedFollowing":
			framers = append(framers, f)
		}
	}
	return framers
}

// countedBuffer is one partition of n rows of distinct-ish values in column 0.
func countedBuffer(n int) sql.WindowBuffer {
	buf := make(sql.WindowBuffer, n)
	for i := range buf {
		buf[i] = sql.Row{int64((i * 7919) % 1009)}
	}
	return buf
}

func TestMinMaxAggEvaluateEachRowOnce(t *testing.T) {
	const n = 1000
	buf := countedBuffer(n)
	partition := []sql.WindowInterval{{Start: 0, End: n}}
	for _, f := range spanningFramers(t) {
		for _, fn := range []func(sql.Expression) sql.WindowFunction{
			func(e sql.Expression) sql.WindowFunction { return NewMaxAgg(e) },
			func(e sql.Expression) sql.WindowFunction { return NewMinAgg(e) },
		} {
			ctx := sql.NewEmptyContext()
			count := 0
			agg := fn(&windowCountingExpr{Expression: expression.NewGetField(0, types.Int64, "x", true), count: &count})
			require.NoError(t, agg.StartPartition(ctx, partition[0], buf))
			for _, frame := range framesOf(t, f.framer, partition, buf)[0] {
				_, err := agg.Compute(ctx, frame, buf)
				require.NoError(t, err)
			}
			require.Equal(t, n, count, f.name)
		}
	}
}

// windowAggBenchmarks are the aggregations BenchmarkWindowAggregates runs.
var windowAggBenchmarks = []struct {
	name string
	new  func(sql.Expression) sql.WindowFunction
}{
	{"max", func(e sql.Expression) sql.WindowFunction { return NewMaxAgg(e) }},
	{"min", func(e sql.Expression) sql.WindowFunction { return NewMinAgg(e) }},
}

// BenchmarkWindowAggregates computes each aggregation over one partition of
// 10,000 rows, under frames that span the partition and one that slides.
func BenchmarkWindowAggregates(b *testing.B) {
	const n = 10_000
	buf := countedBuffer(n)
	partition := sql.WindowInterval{Start: 0, End: n}
	framers := spanningFramers(b)
	for _, f := range windowFramers(b, frameOrderBy) {
		if f.name == "RowsNPrecedingToNFollowing" {
			framers = append(framers, f)
		}
	}
	for _, agg := range windowAggBenchmarks {
		for _, f := range framers {
			b.Run(agg.name+"/"+f.name, func(b *testing.B) {
				ctx := sql.NewEmptyContext()
				fn := agg.new(expression.NewGetField(0, types.Int64, "x", true))
				for b.Loop() {
					require.NoError(b, fn.StartPartition(ctx, partition, buf))
					framer, err := f.framer.NewFramer(partition)
					require.NoError(b, err)
					for {
						frame, err := framer.Next(ctx, buf)
						if errors.Is(err, io.EOF) {
							break
						}
						if _, err := fn.Compute(ctx, frame, buf); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}
