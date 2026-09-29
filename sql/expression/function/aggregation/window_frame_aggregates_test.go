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
	"math"
	"math/big"
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

// countedBuffer is one partition of n rows: an ordering column that rises with
// repeats, and distinct-ish values in column 1.
func countedBuffer(n int) sql.WindowBuffer {
	buf := make(sql.WindowBuffer, n)
	for i := range buf {
		buf[i] = sql.Row{int64(i / 2), int64((i * 7919) % 1009)}
	}
	return buf
}

// framedRows is the number of rows that some frame of the partition holds.
func framedRows(n int, frames []sql.WindowInterval) int {
	framed := make([]bool, n)
	for _, frame := range frames {
		for i := frame.Start; i < frame.End; i++ {
			framed[i] = true
		}
	}
	count := 0
	for _, f := range framed {
		if f {
			count++
		}
	}
	return count
}

// requireEvaluatesEachFramedRowOnce drives an aggregation through every framer
// and requires it to evaluate each row that some frame holds exactly once, and
// no other row: rescanning frames would evaluate rows many times over, and
// rows between frames, such as under N FOLLOWING to N FOLLOWING, are in none.
func requireEvaluatesEachFramedRowOnce(t *testing.T, new func(sql.Expression) sql.WindowFunction) {
	const n = 1000
	buf := countedBuffer(n)
	partition := []sql.WindowInterval{{Start: 0, End: n}}
	for _, f := range windowFramers(t, frameOrderBy) {
		t.Run(f.name, func(t *testing.T) {
			ctx := sql.NewEmptyContext()
			count := 0
			agg := new(&windowCountingExpr{Expression: expression.NewGetField(1, types.Int64, "x", true), count: &count})
			require.NoError(t, agg.StartPartition(ctx, partition[0], buf))
			frames := framesOf(t, f.framer, partition, buf)[0]
			for _, frame := range frames {
				_, err := agg.Compute(ctx, frame, buf)
				require.NoError(t, err)
			}
			require.Equal(t, framedRows(n, frames), count)
		})
	}
}

func TestMinMaxAggEvaluateEachFramedRowOnce(t *testing.T) {
	t.Run("max", func(t *testing.T) {
		requireEvaluatesEachFramedRowOnce(t, func(e sql.Expression) sql.WindowFunction { return NewMaxAgg(e) })
	})
	t.Run("min", func(t *testing.T) {
		requireEvaluatesEachFramedRowOnce(t, func(e sql.Expression) sql.WindowFunction { return NewMinAgg(e) })
	})
}

// windowAggBenchmarks are the aggregations BenchmarkWindowAggregates runs.
var windowAggBenchmarks = []struct {
	name string
	new  func(sql.Expression) sql.WindowFunction
}{
	{"max", func(e sql.Expression) sql.WindowFunction { return NewMaxAgg(e) }},
	{"min", func(e sql.Expression) sql.WindowFunction { return NewMinAgg(e) }},
	{"stddev_pop", func(e sql.Expression) sql.WindowFunction { return NewStdDevPopAgg(e) }},
	{"var_samp", func(e sql.Expression) sql.WindowFunction { return NewVarSampAgg(e) }},
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
				fn := agg.new(expression.NewGetField(1, types.Int64, "x", true))
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

// stdAggs are STD and VARIANCE, each with its answer from a frame's count of
// non-NULL values and the sum of their squared deviations from their mean.
var stdAggs = []struct {
	name string
	new  func(sql.Expression) sql.WindowFunction
	want func(n int, m2 float64) interface{}
}{
	{"stddev_pop", func(e sql.Expression) sql.WindowFunction { return NewStdDevPopAgg(e) }, func(n int, m2 float64) interface{} {
		if n == 0 {
			return nil
		}
		return math.Sqrt(m2 / float64(n))
	}},
	{"stddev_samp", func(e sql.Expression) sql.WindowFunction { return NewStdDevSampAgg(e) }, func(n int, m2 float64) interface{} {
		if n <= 1 {
			return nil
		}
		return math.Sqrt(m2 / float64(n-1))
	}},
	{"var_pop", func(e sql.Expression) sql.WindowFunction { return NewVarPopAgg(e) }, func(n int, m2 float64) interface{} {
		if n == 0 {
			return nil
		}
		return m2 / float64(n)
	}},
	{"var_samp", func(e sql.Expression) sql.WindowFunction { return NewVarSampAgg(e) }, func(n int, m2 float64) interface{} {
		if n <= 1 {
			return nil
		}
		return m2 / float64(n-1)
	}},
}

// exactMoments is a frame's count of non-NULL values in column 1 and the sum
// of their squared deviations from their mean, computed in 256-bit floats.
func exactMoments(buf sql.WindowBuffer, frame sql.WindowInterval) (int, float64) {
	sum, squares := new(big.Float).SetPrec(256), new(big.Float).SetPrec(256)
	n := 0
	for _, row := range buf[frame.Start:frame.End] {
		if row[1] == nil {
			continue
		}
		x := new(big.Float).SetPrec(256).SetFloat64(row[1].(float64))
		sum.Add(sum, x)
		squares.Add(squares, new(big.Float).SetPrec(256).Mul(x, x))
		n++
	}
	if n == 0 {
		return 0, 0
	}
	sum.Mul(sum, sum)
	sum.Quo(sum, new(big.Float).SetPrec(256).SetInt64(int64(n)))
	m2, _ := squares.Sub(squares, sum).Float64()
	return n, m2
}

// stdBuffers are frameBuffer's ordering column beside values that defeat a
// variance taken by subtracting running sums: values that drift steadily and
// are not whole, so that the sums round and a late frame's deviations are tiny
// beside them; a large first value followed by small ones; and large values
// close together.
func stdBuffers() map[string]sql.WindowBuffer {
	base := frameBuffer()
	values := map[string]func(i, row int) interface{}{
		"random with NULLs": func(i, row int) interface{} {
			if base[row][1] == nil {
				return nil
			}
			return float64(base[row][1].(int64))
		},
		"steady drift":          func(i, row int) interface{} { return float64(i) * 1000.3 },
		"large first value":     func(i, row int) interface{} { return map[bool]float64{true: 1e12, false: float64(i % 7)}[i == 0] },
		"large values close by": func(i, row int) interface{} { return 1e9 + float64((i*7919)%101) },
	}
	bufs := make(map[string]sql.WindowBuffer, len(values))
	for name, value := range values {
		buf := make(sql.WindowBuffer, len(base))
		for _, p := range framePartitions {
			for row := p.Start; row < p.End; row++ {
				buf[row] = sql.Row{base[row][0], value(row-p.Start, row)}
			}
		}
		bufs[name] = buf
	}
	return bufs
}

func TestStdAndVarAggMatchExactMoments(t *testing.T) {
	for bufName, buf := range stdBuffers() {
		shapes := map[string][][]sql.WindowInterval{"backwards": backwardFrames(framePartitions)}
		for _, f := range windowFramers(t, frameOrderBy) {
			shapes[f.name] = framesOf(t, f.framer, framePartitions, buf)
		}
		for _, agg := range stdAggs {
			for shapeName, frames := range shapes {
				t.Run(bufName+"/"+agg.name+"/"+shapeName, func(t *testing.T) {
					ctx := sql.NewEmptyContext()
					fn := agg.new(expression.NewGetField(1, types.Float64, "x", true))
					for i, p := range framePartitions {
						require.NoError(t, fn.StartPartition(ctx, p, buf))
						for _, frame := range frames[i] {
							actual, err := fn.Compute(ctx, frame, buf)
							require.NoError(t, err)
							want := agg.want(exactMoments(buf, frame))
							switch {
							case want == nil:
								require.Nil(t, actual, "frame %v", frame)
							case want.(float64) == 0:
								require.Equal(t, 0.0, actual, "frame %v", frame)
							default:
								require.InEpsilon(t, want, actual, 1e-12, "frame %v", frame)
							}
						}
					}
				})
			}
		}
	}
}

func TestStdAndVarAggEvaluateEachFramedRowOnce(t *testing.T) {
	for _, agg := range stdAggs {
		t.Run(agg.name, func(t *testing.T) { requireEvaluatesEachFramedRowOnce(t, agg.new) })
	}
}

// frameAggs are every aggregation that keeps state from one frame to the next.
var frameAggs = append([]struct {
	name string
	new  func(sql.Expression) sql.WindowFunction
}{
	{"max", func(e sql.Expression) sql.WindowFunction { return NewMaxAgg(e) }},
	{"min", func(e sql.Expression) sql.WindowFunction { return NewMinAgg(e) }},
}, func() []struct {
	name string
	new  func(sql.Expression) sql.WindowFunction
} {
	var aggs []struct {
		name string
		new  func(sql.Expression) sql.WindowFunction
	}
	for _, agg := range stdAggs {
		aggs = append(aggs, struct {
			name string
			new  func(sql.Expression) sql.WindowFunction
		}{agg.name, agg.new})
	}
	return aggs
}()...)

// failingExpr fails its nth evaluation.
type failingExpr struct {
	sql.Expression
	failAt, evals int
}

func (e *failingExpr) Eval(ctx *sql.Context, row sql.Row) (interface{}, error) {
	e.evals++
	if e.evals == e.failAt {
		return nil, errEvalFailed
	}
	return e.Expression.Eval(ctx, row)
}

var errEvalFailed = errors.New("eval failed")

func TestFrameAggsSurfaceEvaluationErrors(t *testing.T) {
	buf := countedBuffer(10)
	for _, agg := range frameAggs {
		t.Run(agg.name, func(t *testing.T) {
			ctx := sql.NewEmptyContext()
			fn := agg.new(&failingExpr{Expression: expression.NewGetField(1, types.Int64, "x", true), failAt: 5})
			require.NoError(t, fn.StartPartition(ctx, sql.WindowInterval{Start: 0, End: 10}, buf))
			_, err := fn.Compute(ctx, sql.WindowInterval{Start: 0, End: 10}, buf)
			require.ErrorIs(t, err, errEvalFailed)
		})
	}
}

// A value that does not convert to DOUBLE counts as 0 and warns once, however
// many frames hold it.
func TestStdAndVarAggWarnOncePerUnconvertibleValue(t *testing.T) {
	buf := sql.WindowBuffer{{"a"}, {"b"}, {"3"}, {"4"}}
	for _, agg := range stdAggs {
		t.Run(agg.name, func(t *testing.T) {
			ctx := sql.NewEmptyContext()
			fn := agg.new(expression.NewGetField(0, types.LongText, "x", true))
			require.NoError(t, fn.StartPartition(ctx, sql.WindowInterval{Start: 0, End: 4}, buf))
			for _, frame := range []sql.WindowInterval{{Start: 0, End: 2}, {Start: 0, End: 4}, {Start: 1, End: 4}, {Start: 2, End: 4}} {
				_, err := fn.Compute(ctx, frame, buf)
				require.NoError(t, err)
			}
			require.Equal(t, uint16(2), ctx.WarningCount())
			for _, w := range ctx.Warnings() {
				require.Equal(t, 1292, w.Code)
			}
		})
	}
}

// WithWindow copies an aggregation, and a copy must not share the frame state
// of the one it was copied from.
func TestFrameAggsWithWindowDoNotShareState(t *testing.T) {
	first := sql.WindowBuffer{{int64(0), int64(5)}, {int64(0), int64(1)}, {int64(1), int64(4)}, {int64(1), int64(2)}, {int64(2), int64(3)}, {int64(2), int64(9)}}
	second := sql.WindowBuffer{{int64(0), int64(100)}, {int64(0), int64(700)}, {int64(1), int64(300)}, {int64(1), int64(900)}, {int64(2), int64(200)}, {int64(2), int64(800)}}
	whole := sql.WindowInterval{Start: 0, End: 6}
	later := sql.WindowInterval{Start: 1, End: 6}
	for _, agg := range frameAggs {
		t.Run(agg.name, func(t *testing.T) {
			ctx := sql.NewEmptyContext()
			x := expression.NewGetField(1, types.Int64, "x", true)
			want := agg.new(x)
			require.NoError(t, want.StartPartition(ctx, whole, first))
			_, err := want.Compute(ctx, whole, first)
			require.NoError(t, err)
			expected, err := want.Compute(ctx, later, first)
			require.NoError(t, err)

			used := agg.new(x)
			require.NoError(t, used.StartPartition(ctx, whole, first))
			_, err = used.Compute(ctx, whole, first)
			require.NoError(t, err)
			copied, err := used.(interface {
				WithWindow(*sql.Context, *sql.WindowDefinition) (sql.WindowFunction, error)
			}).WithWindow(ctx, &sql.WindowDefinition{})
			require.NoError(t, err)
			require.NoError(t, copied.StartPartition(ctx, whole, second))
			_, err = copied.Compute(ctx, whole, second)
			require.NoError(t, err)

			actual, err := used.Compute(ctx, later, first)
			require.NoError(t, err)
			require.Equal(t, expected, actual)
		})
	}
}
