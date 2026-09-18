package types

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestHistoricalRecoveryFormats(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{{"true", 1}, {"false", 0}, {"1", 1}, {"0", 0}} {
		t.Run(tc.raw, func(t *testing.T) {
			event := fmt.Sprintf(`{"id":215,"rule_name":"long transaction","is_recovered":%s,"recover_time":123}`, tc.raw)
			var detail N9eResponse[AlertHisEvent]
			if err := json.Unmarshal([]byte(`{"dat":`+event+`,"err":""}`), &detail); err != nil {
				t.Fatal(err)
			}
			var page PageResp[AlertHisEvent]
			if err := json.Unmarshal([]byte(`{"list":[`+event+`],"total":1}`), &page); err != nil {
				t.Fatal(err)
			}
			for _, got := range []AlertHisEvent{detail.Dat, page.List[0]} {
				if got.IsRecovered != tc.want || got.Id != 215 || got.RuleName != "long transaction" || got.RecoverTime != 123 {
					t.Fatalf("incorrect event: %+v", got)
				}
				b, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]any
				json.Unmarshal(b, &fields)
				if fields["is_recovered"] != float64(tc.want) {
					t.Fatalf("output not normalized: %s", b)
				}
			}
		})
	}
}

func TestHistoricalRecoveryRejectsInvalidValues(t *testing.T) {
	for _, raw := range []string{`2`, `-1`, `0.5`, `"true"`, `"1"`, `null`, `{}`, `[]`} {
		t.Run(raw, func(t *testing.T) {
			var event AlertHisEvent
			if err := json.Unmarshal([]byte(`{"is_recovered":`+raw+`}`), &event); err == nil {
				t.Fatal("accepted invalid recovery status")
			}
		})
	}
}
