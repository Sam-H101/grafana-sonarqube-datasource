package plugin

import (
	"strconv"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/sonar"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// The API returns every measure as a string. A column turns the values of
// one metric into a typed field, with the unit and value mappings that make
// the metric readable in a panel:
//
//	INT, FLOAT, PERCENT, MILLISEC, WORK_DUR, RATING  number
//	BOOL                                             boolean
//	LEVEL, STRING, DATA, DISTRIB                     text
//
// A metric missing from the server's catalogue is a number when all its
// values parse as numbers, else text.
type column struct {
	metric sonar.Metric
	key    string
	raw    []*string
}

func newColumn(m sonar.Metric, key string, n int) *column {
	return &column{metric: m, key: key, raw: make([]*string, n)}
}

func (c *column) set(i int, raw *string) { c.raw[i] = raw }
func (c *column) append(raw *string)     { c.raw = append(c.raw, raw) }

func isNumberType(t string) bool {
	switch t {
	case "INT", "FLOAT", "PERCENT", "MILLISEC", "WORK_DUR", "RATING":
		return true
	}
	return false
}

func (c *column) numbers() ([]*float64, bool) {
	out := make([]*float64, len(c.raw))
	for i, r := range c.raw {
		if r == nil {
			continue
		}
		v, err := strconv.ParseFloat(*r, 64)
		if err != nil {
			return nil, false
		}
		out[i] = &v
	}
	return out, true
}

var ratingMapping = data.ValueMappings{data.ValueMapper{
	"1": {Text: "A", Color: "green"},
	"2": {Text: "B", Color: "light-green"},
	"3": {Text: "C", Color: "yellow"},
	"4": {Text: "D", Color: "orange"},
	"5": {Text: "E", Color: "red"},
}}

// levelMapping covers quality gate statuses.
var levelMapping = data.ValueMappings{data.ValueMapper{
	"OK":    {Text: "Passed", Color: "green"},
	"WARN":  {Text: "Warning", Color: "orange"},
	"ERROR": {Text: "Failed", Color: "red"},
	"NONE":  {Text: "Not computed", Color: "gray"},
}}

// field builds the typed field. Its name is the metric key; its config
// carries the unit and mappings.
func (c *column) field() *data.Field {
	cfg := &data.FieldConfig{}
	var f *data.Field

	switch t := c.metric.Type; {
	case t == "BOOL":
		vals := make([]*bool, len(c.raw))
		for i, r := range c.raw {
			if r != nil {
				b := *r == "true"
				vals[i] = &b
			}
		}
		f = data.NewField(c.key, nil, vals)
	case isNumberType(t) || t == "":
		if nums, ok := c.numbers(); ok {
			f = data.NewField(c.key, nil, nums)
			switch t {
			case "PERCENT":
				cfg.Unit = "percent"
			case "MILLISEC":
				cfg.Unit = "ms"
			case "WORK_DUR":
				// Technical debt and effort are in minutes.
				cfg.Unit = "m"
			case "RATING":
				cfg.Mappings = ratingMapping
				cfg.SetMin(1)
				cfg.SetMax(5)
			case "INT":
				cfg.SetDecimals(0)
			}
			break
		}
		fallthrough
	default:
		f = data.NewField(c.key, nil, append([]*string(nil), c.raw...))
		if t == "LEVEL" {
			cfg.Mappings = levelMapping
		}
	}
	f.Config = cfg
	return f
}
