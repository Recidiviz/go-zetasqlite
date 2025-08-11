package internal

import (
	"container/list"
	"encoding/base64"
	"fmt"
	"github.com/Recidiviz/go-zetasqlite/internal/codec"
	"github.com/goccy/go-json"
	"google.golang.org/protobuf/proto"
	"math/big"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func bytesToCacheKeyString(data []byte) string {
	// Option 3: Direct string conversion (creates copy, safe)
	return base64.StdEncoding.EncodeToString(data)
}

// Object pools
var (
	intValuePool = sync.Pool{
		New: func() interface{} { return &IntValue{} },
	}
	floatValuePool = sync.Pool{
		New: func() interface{} { return &FloatValue{} },
	}
	stringValuePool = sync.Pool{
		New: func() interface{} { return &StringValue{} },
	}
	dateValuePool = sync.Pool{
		New: func() interface{} { return &DateValue{} },
	}
)

// Thread-safe LRU Cache with proper type safety
type LRUCache struct {
	capacity int
	cache    map[string]*list.Element
	lruList  *list.List
	mutex    sync.RWMutex
}

type cacheEntry struct {
	key   string
	value Value
}

func NewLRUCache(capacity int) *LRUCache {
	return &LRUCache{
		capacity: capacity,
		cache:    make(map[string]*list.Element),
		lruList:  list.New(),
	}
}

func (c *LRUCache) Get(key string) (Value, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if elem, ok := c.cache[key]; ok {
		// Move to front (most recently used)
		c.lruList.MoveToFront(elem)
		entry := elem.Value.(*cacheEntry)
		return entry.value, true
	}
	return nil, false
}

func (c *LRUCache) GetByBytes(keyBytes []byte) (Value, bool) {
	keyStr := bytesToCacheKeyString(keyBytes)
	//print(keyStr + "\n")
	return c.Get(keyStr)
}

func (c *LRUCache) Put(key string, value Value) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if elem, ok := c.cache[key]; ok {
		// Update existing entry
		c.lruList.MoveToFront(elem)
		entry := elem.Value.(*cacheEntry)
		entry.value = value
		return
	}

	// Add new entry
	entry := &cacheEntry{key: key, value: value}
	elem := c.lruList.PushFront(entry)
	c.cache[key] = elem

	// Evict if over capacity
	if c.lruList.Len() > c.capacity {
		c.evictLRU()
	}
}

func (c *LRUCache) PutByBytes(keyBytes []byte, value Value) {
	keyStr := bytesToCacheKeyString(keyBytes)
	c.Put(keyStr, value)
}

func (c *LRUCache) evictLRU() {
	elem := c.lruList.Back()
	if elem != nil {
		c.lruList.Remove(elem)
		entry := elem.Value.(*cacheEntry)
		delete(c.cache, entry.key)

		// Return evicted object to pool if applicable
		c.returnToPool(entry.value)
	}
}

func (c *LRUCache) Remove(key string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if elem, ok := c.cache[key]; ok {
		c.lruList.Remove(elem)
		entry := elem.Value.(*cacheEntry)
		delete(c.cache, key)
		c.returnToPool(entry.value)
	}
}

func (c *LRUCache) returnToPool(_ Value) {
	//switch v := value.(type) {
	//case *IntValue:
	//	v.Reset()
	//	intValuePool.Put(v)
	//case *FloatValue:
	//	v.Reset()
	//	floatValuePool.Put(v)
	//case *StringValue:
	//	v.Reset()
	//	stringValuePool.Put(v)
	//case *DateValue:
	//	v.Reset()
	//	dateValuePool.Put(v)
	//}

	//switch v := value.(type) {
	//case *IntValue:
	//	v.Reset()
	//	intValuePool.Put(v)
	//case *FloatValue:
	//	v.Reset()
	//	floatValuePool.Put(v)
	//case *StringValue:
	//	v.Reset()
	//	stringValuePool.Put(v)
	//case *DateValue:
	//	v.Reset()
	//	dateValuePool.Put(v)
	//}
	return
}

type BytesCacheService struct {
	cache *LRUCache
}

func NewBytesCacheService(cacheSize int) *BytesCacheService {
	return &BytesCacheService{
		cache: NewLRUCache(cacheSize),
	}
}

func (bcs *BytesCacheService) GetOrDeserialize(protoBytes []byte) (Value, error) {
	// Check cache first
	if cached, _ := bcs.cache.GetByBytes(protoBytes); cached != nil {
		return cached, nil
	}

	// Deserialize
	var layout codec.ValueLayout
	if err := proto.Unmarshal(protoBytes, &layout); err != nil {
		return nil, fmt.Errorf("failed to get Value layout: %w", err)
	}
	value, err := decodeFromValueLayout(&layout)
	if err != nil {
		return nil, fmt.Errorf("failed to decode Value layout: %w", err)
	}

	// Cache the deserialized object
	bcs.cache.PutByBytes(protoBytes, value)
	return value, nil
}

func (bcs *BytesCacheService) returnToPool(obj Value) {
	obj.Reset()
	switch obj.(type) {
	case IntValue:
		intValuePool.Put(obj.(*IntValue))
	case FloatValue:
		floatValuePool.Put(obj.(*IntValue))
	case StringValue:
		stringValuePool.Put(obj.(*IntValue))
	case DateValue:
		dateValuePool.Put(obj.(*IntValue))
	}
}

func decodeFromValueLayout(layout *codec.ValueLayout) (Value, error) {
	switch layout.Header {
	case codec.ValueType_STRING_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.StringValue, 1)
		return &StringValue{layout.Body}, nil
	case codec.ValueType_BYTES_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.BytesValue, 1)
		decoded, err := base64.StdEncoding.DecodeString(layout.Body)
		if err != nil {
			return nil, err
		}
		return &BytesValue{decoded}, nil
	case codec.ValueType_NUMERIC_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.NumericValue, 1)
		r := new(big.Rat)
		r.SetString(layout.Body)
		return &NumericValue{Rat: r}, nil
	case codec.ValueType_BIG_NUMERIC_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.BigNumericValue, 1)
		r := new(big.Rat)
		r.SetString(layout.Body)
		return &NumericValue{Rat: r, isBigNumeric: true}, nil
	case codec.ValueType_DATE_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.DateValue, 1)
		t, err := parseDate(layout.Body)
		if err != nil {
			return nil, err
		}
		return &DateValue{t}, nil
	case codec.ValueType_DATETIME_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.DatetimeValue, 1)
		t, err := parseDatetime(layout.Body)
		if err != nil {
			return nil, err
		}
		return &DatetimeValue{t}, nil
	case codec.ValueType_TIME_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.TimeValue, 1)
		t, err := parseTime(layout.Body)
		if err != nil {
			return nil, err
		}
		return &TimeValue{t}, nil
	case codec.ValueType_TIMESTAMP_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.TimestampValue, 1)
		microsec, err := strconv.ParseInt(layout.Body, 10, 64)
		microSecondsInSecond := int64(time.Second) / int64(time.Microsecond)
		sec := microsec / microSecondsInSecond
		remainder := microsec - (sec * microSecondsInSecond)
		if err != nil {
			return nil, fmt.Errorf("failed to parse unixmicro for timestamp Value %s: %w", layout.Body, err)
		}
		return &TimestampValue{time.Unix(sec, remainder*int64(time.Microsecond))}, nil
	case codec.ValueType_INTERVAL_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.IntervalValue, 1)
		return parseInterval(layout.Body)
	case codec.ValueType_JSON_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.JsonValue, 1)
		return &JsonValue{layout.Body}, nil
	case codec.ValueType_ARRAY_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.ArrayValue, 1)
		var arr []interface{}
		if err := json.Unmarshal([]byte(layout.Body), &arr); err != nil {
			return nil, fmt.Errorf("failed to decode array body: %w", err)
		}
		ret := &ArrayValue{
			values: make([]Value, 0, len(arr)),
		}
		for _, elem := range arr {
			value, err := DecodeValue(elem)
			if err != nil {
				return nil, err
			}
			ret.values = append(ret.values, value)
		}
		return ret, nil
	case codec.ValueType_STRUCT_VALUE_TYPE:
		atomic.AddUint64(&ValueInstantiationCounters.StructValue, 1)
		var structLayout codec.StructValueLayout
		if err := json.Unmarshal([]byte(layout.Body), &structLayout); err != nil {
			return nil, err
		}
		m := map[string]Value{}
		values := make([]Value, 0, len(structLayout.Values))
		for i, data := range structLayout.Values {
			value, err := DecodeValue(data)
			if err != nil {
				return nil, err
			}
			m[structLayout.Keys[i]] = value
			values = append(values, value)
		}
		ret := &StructValue{}
		ret.keys = structLayout.Keys
		ret.values = values
		ret.m = m
		return ret, nil
	}
	return nil, fmt.Errorf("unexpected Value header: %s", layout.Header)
}
