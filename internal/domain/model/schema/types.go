package schema

import "sort"

// DataType represents a primitive data type in the entity model.
type DataType int

const (
	// Numeric types

	// Byte is an 8-bit signed integer.
	Byte DataType = iota
	// Short is a 16-bit signed integer.
	Short
	// Integer is a 32-bit signed integer.
	Integer
	// Long is a 64-bit signed integer.
	Long
	// BigInteger is an arbitrary-precision integer.
	BigInteger
	// UnboundInteger is an integer with no declared bound.
	UnboundInteger
	// Float is a 32-bit IEEE 754 floating-point number.
	Float
	// Double is a 64-bit IEEE 754 floating-point number.
	Double
	// BigDecimal is an arbitrary-precision decimal.
	BigDecimal
	// UnboundDecimal is a decimal with no declared bound.
	UnboundDecimal

	// Text types

	// String is a variable-length character sequence.
	String
	// Character is a single Unicode character.
	Character

	// Temporal types

	// LocalDate is a date without time-zone (yyyy-MM-dd).
	LocalDate
	// LocalDateTime is a date-time without time-zone.
	LocalDateTime
	// LocalTime is a time without time-zone.
	LocalTime
	// ZonedDateTime is a date-time with a time-zone.
	ZonedDateTime
	// Year is a year value (e.g. 2026).
	Year
	// YearMonth is a year-month value (e.g. 2026-03).
	YearMonth

	// Identifier types

	// UUIDType is a universally unique identifier.
	UUIDType
	// TimeUUIDType is a time-based UUID.
	TimeUUIDType

	// Binary

	// ByteArray is a variable-length byte sequence.
	ByteArray

	// Other

	// Boolean is a true/false value.
	Boolean
	// Null represents the absence of a value.
	Null
)

var dataTypeNames = map[DataType]string{
	Byte: "BYTE", Short: "SHORT", Integer: "INTEGER", Long: "LONG",
	BigInteger: "BIG_INTEGER", UnboundInteger: "UNBOUND_INTEGER",
	Float: "FLOAT", Double: "DOUBLE", BigDecimal: "BIG_DECIMAL",
	UnboundDecimal: "UNBOUND_DECIMAL", String: "STRING", Character: "CHARACTER",
	LocalDate: "LOCAL_DATE", LocalDateTime: "LOCAL_DATE_TIME",
	LocalTime: "LOCAL_TIME", ZonedDateTime: "ZONED_DATE_TIME",
	Year: "YEAR", YearMonth: "YEAR_MONTH",
	UUIDType: "UUID_TYPE", TimeUUIDType: "TIME_UUID_TYPE",
	ByteArray: "BYTE_ARRAY", Boolean: "BOOLEAN", Null: "NULL",
}

var dataTypeFromName map[string]DataType

func init() {
	dataTypeFromName = make(map[string]DataType, len(dataTypeNames))
	for dt, name := range dataTypeNames {
		dataTypeFromName[name] = dt
	}
}

// String returns the canonical name of the DataType.
func (d DataType) String() string {
	if name, ok := dataTypeNames[d]; ok {
		return name
	}
	return "UNKNOWN"
}

// ParseDataType returns the DataType for a given name, or false if unknown.
func ParseDataType(name string) (DataType, bool) {
	dt, ok := dataTypeFromName[name]
	return dt, ok
}

// TypeSet is a sorted, deduplicated set of DataTypes.
type TypeSet struct {
	types []DataType
}

// NewTypeSet returns an empty TypeSet.
func NewTypeSet() *TypeSet {
	return &TypeSet{}
}

// Add inserts a DataType into the set. Duplicates are ignored.
// When adding a numeric type, if an existing type belongs to the same numeric
// family (integer or decimal), only the wider type is kept.
func (ts *TypeSet) Add(dt DataType) {
	fam := NumericFamily(dt)
	if fam != 0 {
		rank := NumericRank(dt)
		for i, existing := range ts.types {
			if existing == dt {
				return
			}
			if NumericFamily(existing) == fam {
				if NumericRank(existing) >= rank {
					// Existing is already wider; nothing to do.
					return
				}
				// Replace narrower with wider.
				ts.types[i] = dt
				sort.Slice(ts.types, func(a, b int) bool { return ts.types[a] < ts.types[b] })
				return
			}
		}
	} else {
		for _, existing := range ts.types {
			if existing == dt {
				return
			}
		}
	}
	ts.types = append(ts.types, dt)
	sort.Slice(ts.types, func(i, j int) bool { return ts.types[i] < ts.types[j] })
}

// NumericFamily returns 1 for integer types, 2 for decimal types, 0 for non-numeric.
func NumericFamily(dt DataType) int {
	switch dt {
	case Byte, Short, Integer, Long, BigInteger, UnboundInteger:
		return 1
	case Float, Double, BigDecimal, UnboundDecimal:
		return 2
	default:
		return 0
	}
}

// NumericRank returns the position in the widening hierarchy within a family.
func NumericRank(dt DataType) int {
	switch dt {
	case Byte:
		return 0
	case Short:
		return 1
	case Integer:
		return 2
	case Long:
		return 3
	case BigInteger:
		return 4
	case UnboundInteger:
		return 5
	case Float:
		return 0
	case Double:
		return 1
	case BigDecimal:
		return 2
	case UnboundDecimal:
		return 3
	default:
		return -1
	}
}

// Types returns a sorted copy of the DataTypes in this set.
func (ts *TypeSet) Types() []DataType {
	result := make([]DataType, len(ts.types))
	copy(result, ts.types)
	return result
}

// IsPolymorphic returns true if the set contains more than one type.
func (ts *TypeSet) IsPolymorphic() bool {
	return len(ts.types) > 1
}

// IsEmpty returns true if the set contains no types.
func (ts *TypeSet) IsEmpty() bool {
	return len(ts.types) == 0
}

// Equal returns true if other contains exactly the same DataTypes.
func (ts *TypeSet) Equal(other *TypeSet) bool {
	if len(ts.types) != len(other.types) {
		return false
	}
	for i, dt := range ts.types {
		if dt != other.types[i] {
			return false
		}
	}
	return true
}

// Union returns a new TypeSet containing all types from both sets.
func Union(a, b *TypeSet) *TypeSet {
	result := NewTypeSet()
	for _, dt := range a.types {
		result.Add(dt)
	}
	for _, dt := range b.types {
		result.Add(dt)
	}
	return result
}
