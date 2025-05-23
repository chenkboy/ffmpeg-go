package main

import "github.com/asticode/go-astiav"

func main() {
	//分配重采样上下文
	swrCtx := astiav.AllocSoftwareResampleContext()
	swrCtx.Free()
}
