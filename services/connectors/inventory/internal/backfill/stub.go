package backfill

import "context"

// StubBackfillClient is a test double for BackfillClient.
// Set Watermark and Err before use to control what GetWatermark returns.
type StubBackfillClient struct {
	Watermark  map[string]any
	Err        error
	Partitions []map[string]any
}

func (s *StubBackfillClient) GetWatermark(_ context.Context, partition map[string]any) (map[string]any, error) {
	s.Partitions = append(s.Partitions, partition)
	if s.Err != nil {
		return nil, s.Err
	}
	return s.Watermark, nil
}
