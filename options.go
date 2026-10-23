package cuttle

import "time"

type Options struct {
	Storage       Storage
	MaxGraphs     int
	MaxRows       int
	MaxBytes      int
	MaxEntities   int
	MaxQueryBytes int
	QueryTimeout  time.Duration
	Clock         func() time.Time
}

func defaults(o Options) Options {
	if o.MaxGraphs <= 0 {
		o.MaxGraphs = 10
	}
	if o.MaxRows <= 0 {
		o.MaxRows = 1000
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = 4 << 20
	}
	if o.MaxEntities <= 0 {
		o.MaxEntities = 10000
	}
	if o.MaxQueryBytes <= 0 {
		o.MaxQueryBytes = 65536
	}
	if o.QueryTimeout <= 0 {
		o.QueryTimeout = 5 * time.Second
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	return o
}
