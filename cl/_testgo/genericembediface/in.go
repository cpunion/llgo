package main

import (
	"github.com/goplus/llgo/cl/_testgo/genericembediface/streamlib"
)

type Request struct{}
type Response struct{}

type ReflectionServer interface {
	ServerReflectionInfo(streamlib.BidiStreamingServer[Request, Response]) error
}

func handler(srv any, stream streamlib.ServerStream) error {
	return srv.(ReflectionServer).ServerReflectionInfo(&streamlib.GenericServerStream[Request, Response]{ServerStream: stream})
}

type server struct{}

func (server) ServerReflectionInfo(streamlib.BidiStreamingServer[Request, Response]) error {
	return nil
}

type stream struct{}

func (stream) Context() string {
	return "Context"
}

func main() {
	_ = handler(server{}, stream{})
	println("pass")
}
