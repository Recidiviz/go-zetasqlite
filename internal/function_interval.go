package internal

import (
	"fmt"

	"cloud.google.com/go/bigquery"
)

func INTERVAL(value int64, part string) (Value, error) {
	switch part {
	case "YEAR":
		return &IntervalValue{value: &bigquery.IntervalValue{Years: int32(value)}}, nil
	case "MONTH":
		return &IntervalValue{value: &bigquery.IntervalValue{Months: int32(value)}}, nil
	case "DAY":
		return &IntervalValue{value: &bigquery.IntervalValue{Days: int32(value)}}, nil
	case "HOUR":
		return &IntervalValue{value: &bigquery.IntervalValue{Hours: int32(value)}}, nil
	case "MINUTE":
		return &IntervalValue{value: &bigquery.IntervalValue{Minutes: int32(value)}}, nil
	case "SECOND":
		return &IntervalValue{value: &bigquery.IntervalValue{Seconds: int32(value)}}, nil
	case "NANOSECOND":
		return &IntervalValue{value: &bigquery.IntervalValue{SubSecondNanos: int32(value)}}, nil
	}
	return nil, fmt.Errorf("unexpected interval part: %s", part)
}

func MAKE_INTERVAL(year, month, day, hour, minute, second int64) (Value, error) {
	return &IntervalValue{
		value: &bigquery.IntervalValue{
			Years:   int32(year),
			Months:  int32(month),
			Days:    int32(day),
			Hours:   int32(hour),
			Minutes: int32(minute),
			Seconds: int32(second),
		},
	}, nil
}

func JUSTIFY_DAYS(v *IntervalValue) (Value, error) {
	if v.value.Days > 29 {
		v.value.Months += v.value.Days / 30
		v.value.Days = v.value.Days % 30
	} else if v.value.Days < -29 {
		v.value.Months += v.value.Days / 30
		v.value.Days = v.value.Days % 30
	}
	if v.value.Months > 11 {
		v.value.Months -= 12
		v.value.Years++
	} else if v.value.Months < -11 {
		v.value.Months += 12
		v.value.Years--
	}
	return v, nil
}

func JUSTIFY_HOURS(v *IntervalValue) (Value, error) {
	if v.value.Seconds > 59 {
		v.value.Minutes += v.value.Seconds / 60
		v.value.Seconds = v.value.Seconds % 60
	} else if v.value.Seconds < -59 {
		v.value.Minutes += v.value.Seconds / 60
		v.value.Seconds = v.value.Seconds % 60
	}
	if v.value.Minutes > 59 {
		v.value.Hours += v.value.Minutes / 60
		v.value.Minutes = v.value.Minutes % 60
	} else if v.value.Minutes < -59 {
		v.value.Hours += v.value.Hours / 60
		v.value.Minutes = v.value.Minutes % 60
	}
	if v.value.Hours > 23 {
		v.value.Days += v.value.Hours / 24
		v.value.Hours = v.value.Hours % 24
	} else if v.value.Hours < -23 {
		v.value.Days += v.value.Hours / 24
		v.value.Hours = v.value.Hours % 24
	}
	return v, nil
}

func JUSTIFY_INTERVAL(v *IntervalValue) (Value, error) {
	if _, err := JUSTIFY_HOURS(v); err != nil {
		return nil, err
	}
	return JUSTIFY_DAYS(v)
}
