module github.com/shynome/pprometheus

go 1.13

require (
	github.com/go-kit/kit v0.9.0
	github.com/gogo/protobuf v1.2.2-0.20190730201129-28a6bbf47e48
	github.com/grpc-ecosystem/grpc-gateway v1.9.5
	github.com/json-iterator/go v1.1.8
	github.com/mwitkow/go-conntrack v0.0.0-20190716064945-2f068394615f
	github.com/oklog/run v1.0.0
	github.com/opentracing-contrib/go-stdlib v0.0.0-20190519235532-cf7a6c988dc9
	github.com/opentracing/opentracing-go v1.1.0
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v1.3.0
	github.com/prometheus/client_model v0.1.0
	github.com/prometheus/common v0.7.0
	// this is 2.15.1
	github.com/prometheus/prometheus v1.8.2-0.20191225005851-8744510c6391
	github.com/soheilhy/cmux v0.1.4
	github.com/stretchr/testify v1.4.0 // indirect
	golang.org/x/net v0.0.0-20190724013045-ca1201d0de80
	google.golang.org/grpc v1.22.1
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	k8s.io/klog v0.4.0
)

replace k8s.io/klog => github.com/simonpasquier/klog-gokit v0.1.0
