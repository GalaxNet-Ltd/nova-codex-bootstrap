package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"
)

func TestSharedHostEventContractFixtures(t *testing.T) {
	data, err := os.ReadFile("../../testdata/host-events-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name  string          `json:"name"`
		Valid bool            `json:"valid"`
		Body  json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			var event HostEvent
			decoder := json.NewDecoder(bytes.NewReader(fixture.Body))
			decoder.DisallowUnknownFields()
			valid := decoder.Decode(&event) == nil && decoder.Decode(new(any)) == io.EOF && event.Valid()
			if valid != fixture.Valid {
				t.Fatal("host event contract changed")
			}
			if valid {
				encoded, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				var roundTrip HostEvent
				if json.Unmarshal(encoded, &roundTrip) != nil || !roundTrip.Valid() {
					t.Fatal("event did not round trip")
				}
			}
		})
	}
}
