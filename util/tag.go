package util

import (
	"strings"

	"github.com/bingoohuang/gg/pkg/ss"
)

type TagValue interface {
	Contains(s string) bool
}

type Tag struct {
	Values []TagValue
}

func (t *Tag) Contains(s string) bool {
	for _, value := range t.Values {
		if value.Contains(s) {
			return true
		}
	}

	return false
}

type SingleValue struct {
	Value string
}

func (v SingleValue) Contains(s string) bool { return strings.EqualFold(v.Value, s) }

type RangeValue struct {
	From string
	To   string

	IsInt   bool
	FromInt int
	ToInt   int
}

func (r *RangeValue) Contains(s string) bool {
	if IsDigits(s) && r.IsInt {
		return r.containsInt(ss.ParseInt(s))
	}
	return r.containsString(s)
}

func (r *RangeValue) containsInt(v int) bool {
	return (r.From == "" || r.FromInt <= v) && (r.To == "" || v <= r.ToInt)
}

func (r *RangeValue) containsString(v string) bool {
	return (r.From == "" || r.From <= v) && (r.To == "" || v <= r.To)
}

func ParseTag(s string) *Tag {
	tag := &Tag{}
	parts := ss.Split(s, ss.WithSeps(","), ss.WithTrimSpace(true), ss.WithIgnoreEmpty(true))
	for _, part := range parts {
		p := strings.Index(part, "-")
		if p < 0 {
			tag.Values = append(tag.Values, &SingleValue{Value: part})
		} else {
			tag.Values = append(tag.Values, NewRangeValue(part[:p], part[p+1:]))
		}
	}

	return tag
}

func NewRangeValue(a, b string) TagValue {
	r := &RangeValue{
		From:    strings.TrimSpace(a),
		To:      strings.TrimSpace(b),
		IsInt:   false,
		FromInt: 0,
		ToInt:   0,
	}

	r.IsInt = (r.From == "" || IsDigits(r.From)) && (r.To == "" || IsDigits(r.To))
	r.FromInt = ss.ParseInt(r.From)
	r.ToInt = ss.ParseInt(r.To)

	if r.From > r.To {
		r.From, r.To = r.To, r.From
	}
	if r.FromInt > r.ToInt {
		r.FromInt, r.ToInt = r.ToInt, r.FromInt
	}

	return r
}

func IsDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
