module github.com/shynome/ppromethus

go 1.13

require (
	github.com/go-kit/kit v0.9.0
	github.com/mwitkow/go-conntrack v0.0.0-20190716064945-2f068394615f
	github.com/oklog/run v1.0.0
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v1.3.0
	github.com/prometheus/common v0.7.0
	// this is 2.15.1
	github.com/prometheus/prometheus v1.8.2-0.20191225005851-8744510c6391
	github.com/simonpasquier/klog-gokit v0.2.0
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	k8s.io/klog v0.4.0
)

replace k8s.io/klog => github.com/simonpasquier/klog-gokit v0.1.0
