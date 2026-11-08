package ingest

import (
	"context"
	"io"
	"net/http"
	"strings"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip" // senders may compress
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// GRPCServer serves OTLP/gRPC metrics and logs. Traces are answered with
// Unimplemented by the default gRPC behaviour for unregistered services.
func (g *Gateway) GRPCServer() *grpc.Server {
	s := grpc.NewServer(grpc.MaxRecvMsgSize(MaxBody))
	colmetrics.RegisterMetricsServiceServer(s, grpcMetrics{g: g})
	collogs.RegisterLogsServiceServer(s, grpcLogs{g: g})
	return s
}

type grpcMetrics struct {
	colmetrics.UnimplementedMetricsServiceServer
	g *Gateway
}

func (s grpcMetrics) Export(ctx context.Context, req *colmetrics.ExportMetricsServiceRequest) (*colmetrics.ExportMetricsServiceResponse, error) {
	return &colmetrics.ExportMetricsServiceResponse{}, s.g.export(ctx, otlpMetrics, req)
}

type grpcLogs struct {
	collogs.UnimplementedLogsServiceServer
	g *Gateway
}

func (s grpcLogs) Export(ctx context.Context, req *collogs.ExportLogsServiceRequest) (*collogs.ExportLogsServiceResponse, error) {
	return &collogs.ExportLogsServiceResponse{}, s.g.export(ctx, otlpLogs, req)
}

func (g *Gateway) export(ctx context.Context, rt route, msg proto.Message) error {
	md, _ := metadata.FromIncomingContext(ctx)
	auth := ""
	if v := md.Get("authorization"); len(v) > 0 {
		auth = v[0]
	}
	name, err := g.sender(auth)
	if err != nil {
		return status.Error(codes.Unauthenticated, err.Error())
	}
	body, err := proto.Marshal(msg)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	header := http.Header{"Content-Type": {"application/x-protobuf"}}
	for _, h := range logFieldHeaders {
		if v := md.Get(strings.ToLower(h)); len(v) > 0 {
			header.Set(h, v[0])
		}
	}
	resp, err := g.forward(ctx, rt, name, nil, header, body)
	if err != nil {
		g.opts.Log.Warn("forward push", "path", rt.path, "sender", name, "err", err)
		return status.Error(codes.Unavailable, "storage unavailable")
	}
	defer resp.Body.Close()
	msgBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return status.Errorf(codes.Unavailable, "storage: %s", resp.Status)
	default:
		return status.Errorf(codes.InvalidArgument, "storage: %s: %s", resp.Status, strings.TrimSpace(string(msgBody)))
	}
}
