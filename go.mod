module github.com/shynome/pprometheus

go 1.13

require (
	github.com/go-kit/kit v0.9.0
	github.com/mwitkow/go-conntrack v0.0.0-20190716064945-2f068394615f
	github.com/oklog/run v1.0.0
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v1.3.0
	github.com/prometheus/common v0.7.0
	// this is 2.15.1
	github.com/prometheus/prometheus v1.0.0
	github.com/stretchr/testify v1.4.0 // indirect
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	k8s.io/klog v0.4.0
)

replace (
	github.com/prometheus/prometheus => ./modules/prometheus
	k8s.io/klog => github.com/simonpasquier/klog-gokit v0.1.0
)
