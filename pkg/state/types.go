package state

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type BillingPeriod string

func ParseBillingPeriod(period string) (*BillingPeriod, error) {
	p := BillingPeriod(period) // TODO: Validation
	return &p, nil
}

func (period *BillingPeriod) IsPastDue() bool {
	parts := strings.Split(string(*period), "-")
	if len(parts) != 2 || len(parts[1]) != 8 {
		panic(fmt.Sprintf("Malformed period format: %s", *period))
	}

	// TODO: Make sure reports timezone is actually UTC
	t, err := time.ParseInLocation("20060102", parts[1], time.UTC)
	if err != nil {
		panic(err)
	}

	return t.Before(time.Now())
}

func (period *BillingPeriod) UnmarshalJSON(bytes []byte) error {
	var s string

	if err := json.Unmarshal(bytes, &s); err != nil {
		return err
	}
	p, err := ParseBillingPeriod(s)

	if err != nil {
		return err
	}

	*period = *p

	return nil
}
