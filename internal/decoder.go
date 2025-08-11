package internal

import (
	"database/sql/driver"
	"fmt"
)

var ValueInstantiationCounters = struct {
	IntValue        uint64
	BoolValue       uint64
	FloatValue      uint64
	StringValue     uint64
	BytesValue      uint64
	NumericValue    uint64
	BigNumericValue uint64
	DateValue       uint64
	DatetimeValue   uint64
	TimeValue       uint64
	TimestampValue  uint64
	ArrayValue      uint64
	StructValue     uint64
	JsonValue       uint64
	IntervalValue   uint64
}{}

var cacheService = NewBytesCacheService(8000)

func DecodeValue(v driver.Value) (Value, error) {
	if isNullValue(v) {
		return nil, nil
	}
	switch vv := v.(type) {
	case int64:
		return &IntValue{vv}, nil
	case float64:
		return &FloatValue{vv}, nil
	case bool:
		return &BoolValue{vv}, nil
	}

	if protoBytes, ok := v.([]byte); ok {
		value, err := cacheService.GetOrDeserialize(protoBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to get Value layout from cache: %w", err)
		}

		return value, nil
	} else {
		return nil, fmt.Errorf("unexpected Value type: %T", v)
	}
}
