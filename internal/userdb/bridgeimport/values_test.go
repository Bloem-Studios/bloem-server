package bridgeimport

import (
	"math"
	"testing"
)

func TestImportValuesRefuseLossyConversion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kind  ColumnKind
		value any
	}{
		{"boolean integer", booleanColumn, int64(2)},
		{"integer text", integerColumn, "4"},
		{"infinite", realColumn, math.Inf(1)},
		{"NUL text", textColumn, "private\x00value"},
		{"timestamp precision", instantColumn, "2026-01-01T00:00:00.000000001Z"},
		{"unknown time", instantColumn, ""},
		{"duplicate JSON", jsonColumn, `{"value":false,"value":true}`},
		{"JSON NUL", jsonColumn, `{"value":"\u0000"}`},
		{"trailing JSON", jsonColumn, `{} true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := importValue(SourceColumn{Kind: tc.kind}, tc.value); err == nil {
				t.Fatal("lossy value accepted")
			}
		})
	}
	for _, tc := range []struct {
		kind  ColumnKind
		value any
	}{
		{booleanColumn, int64(0)}, {booleanColumn, nil}, {integerColumn, int64(7)},
		{instantColumn, "2026-01-01T01:00:00.123456+01:00"},
		{jsonColumn, `{"value":null,"nested":[false,0,""]}`},
		{textColumn, "opaque non-JSON"},
	} {
		if _, err := importValue(SourceColumn{Kind: tc.kind}, tc.value); err != nil {
			t.Fatal(err)
		}
	}
}
