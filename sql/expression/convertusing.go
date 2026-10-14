// Copyright 2023 Dolthub, Inc.
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

package expression

import (
	"fmt"
	"unicode/utf8"

	"github.com/dolthub/vitess/go/mysql"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/encodings"
	"github.com/dolthub/go-mysql-server/sql/types"
)

// ConvertUsing represents a CONVERT(X USING T) operation that casts the expression X to the character set T.
type ConvertUsing struct {
	UnaryExpressionStub
	TargetCharSet sql.CharacterSetID
}

var _ sql.Expression = (*ConvertUsing)(nil)
var _ sql.CollationCoercible = (*ConvertUsing)(nil)

func NewConvertUsing(expr sql.Expression, targetCharSet sql.CharacterSetID) *ConvertUsing {
	return &ConvertUsing{
		UnaryExpressionStub: UnaryExpressionStub{Child: expr},
		TargetCharSet:       targetCharSet,
	}
}

// String implements the interface sql.Expression.
func (c *ConvertUsing) String() string {
	return fmt.Sprintf("CONVERT(%s USING %s)", c.Child.String(), c.TargetCharSet.Name())
}

// Type implements the interface sql.Expression.
func (c *ConvertUsing) Type(ctx *sql.Context) sql.Type {
	typ := c.Child.Type(ctx)
	if collatedType, ok := typ.(sql.TypeWithCollation); ok {
		newTyp, _ := collatedType.WithNewCollation(c.TargetCharSet.DefaultCollation())
		return newTyp
	}
	// A number or a time is converted as its text, so the result is text rather than the argument's type.
	return types.CreateLongText(c.TargetCharSet.DefaultCollation())
}

// Eval implements the interface sql.Expression.
func (c *ConvertUsing) Eval(ctx *sql.Context, row sql.Row) (interface{}, error) {
	val, err := c.Child.Eval(ctx, row)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}

	var valBytes []byte
	switch v := val.(type) {
	case []byte:
		valBytes = v
	case string:
		valBytes = encodings.StringToBytes(v)
	default:
		str, _, err := types.ConvertToCollatedString(ctx, val, c.Child.Type(ctx))
		if err != nil {
			return nil, err
		}
		valBytes = encodings.StringToBytes(str)
	}

	// Strings are stored internally as utf8mb4. A byte sequence that is not
	// valid utf8mb4 cannot be represented in the target character set, so
	// MySQL evaluates the conversion to NULL and issues warning 1300
	// (ER_INVALID_CHARACTER_STRING) instead of producing a value.
	if c.TargetCharSet == sql.CharacterSet_utf8mb4 && !utf8.Valid(valBytes) {
		ctx.Warn(mysql.ERInvalidCharacterString, "%s", sql.ErrCharSetInvalidString.New(c.TargetCharSet.Name(), string(valBytes)))
		return nil, nil
	}

	newString := c.TargetCharSet.Encoder().EncodeReplaceUnknown(valBytes)
	return encodings.BytesToString(newString), nil
}

// WithChildren implements the interface sql.Expression.
func (c *ConvertUsing) WithChildren(ctx *sql.Context, children ...sql.Expression) (sql.Expression, error) {
	if len(children) != 1 {
		return nil, sql.ErrInvalidChildrenNumber.New(c, len(children), 1)
	}
	return NewConvertUsing(children[0], c.TargetCharSet), nil
}

// CollationCoercibility implements the interface sql.CollationCoercible.
func (c *ConvertUsing) CollationCoercibility(ctx *sql.Context) (collation sql.CollationID, coercibility byte) {
	return c.TargetCharSet.DefaultCollation(), 2
}
