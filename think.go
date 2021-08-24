package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// ThinkTime simulates man to think awhile.
type ThinkTime struct {
	Min, Max time.Duration
}

// Think start to be thinking.
func (t ThinkTime) Think(thinkNow bool) (thinkTime time.Duration) {
	if t.Min == t.Max {
		thinkTime = t.Min
	} else {
		thinkTime = time.Duration(RandInt(int64(t.Max-t.Min))) + t.Min
	}

	if thinkNow {
		time.Sleep(thinkTime)
	}

	return thinkTime
}

func RandInt(n int64) int64 {
	result, _ := rand.Int(rand.Reader, big.NewInt(n))
	return result.Int64()
}

func ParseThinkTime(think string) (t *ThinkTime, err error) {
	if think == "" {
		return nil, nil
	}

	t = &ThinkTime{}

	rangePos := strings.Index(think, "-")
	if rangePos < 0 {
		if t.Min, err = time.ParseDuration(think); err != nil {
			return nil, err
		}
		t.Max = t.Min
		return t, nil
	}

	minThink, maxThink := think[0:rangePos], think[rangePos+1:]
	if t.Max, err = time.ParseDuration(maxThink); err != nil {
		return nil, err
	}

	if minThink == "" {
		return t, nil
	}

	if regexp.MustCompile(`^\d+$`).MatchString(minThink) {
		minThink += FindUnit(maxThink)
	}

	if t.Min, err = time.ParseDuration(minThink); err != nil {
		return nil, err
	}

	if t.Min > t.Max {
		return nil, fmt.Errorf("min think time should be less than max")
	}

	return t, nil
}

func FindUnit(s string) string {
	pos := strings.LastIndexFunc(s, func(r rune) bool {
		return r >= '0' && r <= '9'
	})

	if pos < 0 {
		return s
	}

	return s[pos+1:]
}
