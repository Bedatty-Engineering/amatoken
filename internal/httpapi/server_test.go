package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/bedatty/amatoken/internal/rtkgain"
)

func TestNewKeepsNilRTKReader(t *testing.T) {
	var rtk *fakeRTKReader
	s := New(nil, nil, nil, rtk)
	if s.RTKReader != nil {
		t.Fatalf("RTKReader = %#v, want nil", s.RTKReader)
	}
}

type fakeRTKReader struct{}

func (*fakeRTKReader) Summary(context.Context, *time.Time, *time.Time) (*rtkgain.Summary, error) {
	return nil, nil
}

func (*fakeRTKReader) Commands(context.Context, int, string, *time.Time, *time.Time) ([]rtkgain.CommandStat, error) {
	return nil, nil
}

func (*fakeRTKReader) TimeSeries(context.Context, string, *time.Time, *time.Time, string) ([]rtkgain.TimePoint, error) {
	return nil, nil
}
