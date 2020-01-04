package push

import (
	"testing"
	"time"

	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/tsdb"
)

type nopAppender struct{}

func (a nopAppender) Add(labels.Labels, int64, float64) (uint64, error) { return 0, nil }
func (a nopAppender) AddFast(uint64, int64, float64) error              { return nil }
func (a nopAppender) Commit() error                                     { return nil }
func (a nopAppender) Rollback() error                                   { return nil }

func TestLabels(t *testing.T) {
	lb, err := promql.ParseMetric(`ccc {} {b="fewfwe"}`)
	if err != nil {
		t.Error(err)
		return
	}
	t.Log(lb)
}

func TestApppend(t *testing.T) {
	s := &Server{
		GetAppender: func() tsdb.Appender {
			return nopAppender{}
		},
	}
	content := []byte(`
	# fffff
	aaa { b="7" } 1
	ccc 3
	`)
	err := s.Append(content, time.Now())
	if err != nil {
		t.Error(err)
		return
	}
}
