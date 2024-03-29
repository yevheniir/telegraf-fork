package enum

import (
	"testing"
	"time"

	"github.com/yevheniir/telegraf-fork"
	"github.com/yevheniir/telegraf-fork/metric"
	"github.com/stretchr/testify/assert"
)

func createTestMetric() telegraf.Metric {
	metric, _ := metric.New("m1",
		map[string]string{"tag": "tag_value"},
		map[string]interface{}{
			"string_value": "test",
			"int_value":    int(13),
			"true_value":   true,
		},
		time.Now(),
	)
	return metric
}

func calculateProcessedValues(mapper EnumMapper, metric telegraf.Metric) map[string]interface{} {
	processed := mapper.Apply(metric)
	return processed[0].Fields()
}

func calculateProcessedTags(mapper EnumMapper, metric telegraf.Metric) map[string]string {
	processed := mapper.Apply(metric)
	return processed[0].Tags()
}

func assertFieldValue(t *testing.T, expected interface{}, field string, fields map[string]interface{}) {
	value, present := fields[field]
	assert.True(t, present, "value of field '"+field+"' was not present")
	assert.EqualValues(t, expected, value)
}

func assertTagValue(t *testing.T, expected interface{}, tag string, tags map[string]string) {
	value, present := tags[tag]
	assert.True(t, present, "value of tag '"+tag+"' was not present")
	assert.EqualValues(t, expected, value)
}

func TestRetainsMetric(t *testing.T) {
	mapper := EnumMapper{}
	source := createTestMetric()

	target := mapper.Apply(source)[0]
	fields := target.Fields()

	assertFieldValue(t, "test", "string_value", fields)
	assertFieldValue(t, 13, "int_value", fields)
	assertFieldValue(t, true, "true_value", fields)
	assert.Equal(t, "m1", target.Name())
	assert.Equal(t, source.Tags(), target.Tags())
	assert.Equal(t, source.Time(), target.Time())
}

func TestMapsSingleStringValue(t *testing.T) {
	mapper := EnumMapper{Mappings: []Mapping{{Field: "string_value", ValueMappings: map[string]interface{}{"test": int64(1)}}}}

	fields := calculateProcessedValues(mapper, createTestMetric())

	assertFieldValue(t, 1, "string_value", fields)
}

func TestMapsSingleStringValueTag(t *testing.T) {
	mapper := EnumMapper{Mappings: []Mapping{{Tag: "tag", ValueMappings: map[string]interface{}{"tag_value": "valuable"}}}}

	tags := calculateProcessedTags(mapper, createTestMetric())

	assertTagValue(t, "valuable", "tag", tags)
}

func TestNoFailureOnMappingsOnNonStringValuedFields(t *testing.T) {
	mapper := EnumMapper{Mappings: []Mapping{{Field: "int_value", ValueMappings: map[string]interface{}{"13i": int64(7)}}}}

	fields := calculateProcessedValues(mapper, createTestMetric())

	assertFieldValue(t, 13, "int_value", fields)
}

func TestMapSingleBoolValue(t *testing.T) {
	mapper := EnumMapper{Mappings: []Mapping{{Field: "true_value", ValueMappings: map[string]interface{}{"true": int64(1)}}}}

	fields := calculateProcessedValues(mapper, createTestMetric())

	assertFieldValue(t, 1, "true_value", fields)
}

func TestMapsToDefaultValueOnUnknownSourceValue(t *testing.T) {
	mapper := EnumMapper{Mappings: []Mapping{{Field: "string_value", Default: int64(42), ValueMappings: map[string]interface{}{"other": int64(1)}}}}

	fields := calculateProcessedValues(mapper, createTestMetric())

	assertFieldValue(t, 42, "string_value", fields)
}

func TestDoNotMapToDefaultValueKnownSourceValue(t *testing.T) {
	mapper := EnumMapper{Mappings: []Mapping{{Field: "string_value", Default: int64(42), ValueMappings: map[string]interface{}{"test": int64(1)}}}}

	fields := calculateProcessedValues(mapper, createTestMetric())

	assertFieldValue(t, 1, "string_value", fields)
}

func TestNoMappingWithoutDefaultOrDefinedMappingValue(t *testing.T) {
	mapper := EnumMapper{Mappings: []Mapping{{Field: "string_value", ValueMappings: map[string]interface{}{"other": int64(1)}}}}

	fields := calculateProcessedValues(mapper, createTestMetric())

	assertFieldValue(t, "test", "string_value", fields)
}

func TestWritesToDestination(t *testing.T) {
	mapper := EnumMapper{Mappings: []Mapping{{Field: "string_value", Dest: "string_code", ValueMappings: map[string]interface{}{"test": int64(1)}}}}

	fields := calculateProcessedValues(mapper, createTestMetric())

	assertFieldValue(t, "test", "string_value", fields)
	assertFieldValue(t, 1, "string_code", fields)
}

func TestDoNotWriteToDestinationWithoutDefaultOrDefinedMapping(t *testing.T) {
	field := "string_code"
	mapper := EnumMapper{Mappings: []Mapping{{Field: "string_value", Dest: field, ValueMappings: map[string]interface{}{"other": int64(1)}}}}

	fields := calculateProcessedValues(mapper, createTestMetric())

	assertFieldValue(t, "test", "string_value", fields)
	_, present := fields[field]
	assert.False(t, present, "value of field '"+field+"' was present")
}
