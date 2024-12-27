package format

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type inner2 struct {
	Field2 string `url:"field2"`
	Field3 int    `url:"field3"`
}

type inner1 struct {
	Field1 string   `url:"field1"`
	Inners []inner2 `url:"inners"`
}

type outer struct {
	Inner inner1 `url:"inner"`
}

type embedded struct {
	outer
}

type outerWithStrings struct {
	Strings []string `url:"strings"`
}

func TestDecodeURLEncoded(t *testing.T) {
	// Test cases
	tests := []struct {
		name        string
		values      url.Values
		output      any
		expected    any
		expectedErr error
	}{
		{
			name: "simple",
			values: url.Values{
				"inner.field1":          {"value1"},
				"inner.inners.1.field2": {"value2"},
				"inner.inners.1.field3": {"3"},
			},
			output: &outer{},
			expected: &outer{
				Inner: inner1{
					Field1: "value1",
					Inners: []inner2{
						{
							Field2: "value2",
							Field3: 3,
						},
					},
				},
			},
		},
		{
			name: "zero indexed",
			values: url.Values{
				"inner.inners.0.field2": {"value2"},
			},
			output:      &outer{},
			expectedErr: errZeroIndex,
		},
		{
			name: "negative indexed",
			values: url.Values{
				"inner.inners.-42.field2": {"value2"},
			},
			output:      &outer{},
			expectedErr: errZeroIndex,
		},
		{
			name: "no such field",
			values: url.Values{
				"foo": {"bar"},
			},
			output:      &outer{},
			expectedErr: errNoSuchField,
		},
		{
			name: "embedded",
			values: url.Values{
				"inner.field1":          {"value1"},
				"inner.inners.1.field2": {"value2"},
				"inner.inners.1.field3": {"3"},
			},
			output: &embedded{},
			expected: &embedded{
				outer: outer{
					Inner: inner1{
						Field1: "value1",
						Inners: []inner2{
							{
								Field2: "value2",
								Field3: 3,
							},
						},
					},
				},
			},
		},
		{
			name: "slice of strings",
			values: url.Values{
				"strings.1": {"value1"},
				"strings.2": {"value2"},
			},
			output: &outerWithStrings{},
			expected: &outerWithStrings{
				Strings: []string{"value1", "value2"},
			},
		},
	}

	// Run tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := decodeURLEncoded(tt.values, tt.output)
			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, tt.output)
			}
		})
	}
}
