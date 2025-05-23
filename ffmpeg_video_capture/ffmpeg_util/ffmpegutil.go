package ffmpegutil

import (
	"errors"
	"fmt"
	"github.com/asticode/go-astiav"
	"image"
	"image/color"
	"log"
)

// 创建流并复制编码器参数
func CreateStreamAndCopyParams(fmtCtx *astiav.FormatContext, srcStream *astiav.Stream) (*astiav.Stream, error) {
	destStream := fmtCtx.NewStream(nil)
	err := srcStream.CodecParameters().Copy(destStream.CodecParameters())
	if err != nil {
		return nil, err
	}
	destStream.CodecParameters().SetCodecTag(0)
	return destStream, nil
}

// 打开流并查找流信息
func GetInputFormatContext(input string, options *astiav.Dictionary) (*astiav.FormatContext, error) {
	// 打开流
	inputFormatContext := astiav.AllocFormatContext()
	if err := inputFormatContext.OpenInput(input, nil, options); err != nil {
		return nil, err
	}
	// 查找流信息
	if err := inputFormatContext.FindStreamInfo(nil); err != nil {
		return nil, err
	}
	return inputFormatContext, nil
}

// 查找指定类型的媒体流
func FindStream(inputFormatContext *astiav.FormatContext, mediaType astiav.MediaType) *astiav.Stream {
	// 找到视频和音频流
	for _, stream := range inputFormatContext.Streams() {
		if stream.CodecParameters().MediaType() == mediaType {
			//if stream.CodecParameters().CodecType() == mediaType {
			return stream
		}
	}
	return nil
}

// 找到对应的解码器并打开
func FindAndOpenDecoderCtx(stream *astiav.Stream) (*astiav.CodecContext, *astiav.Codec, error) {
	//找到对应的解码器
	codec := astiav.FindDecoder(stream.CodecParameters().CodecID())

	codecContext := astiav.AllocCodecContext(codec)
	//更新解码器参数
	if err := codecContext.FromCodecParameters(stream.CodecParameters()); err != nil {
		log.Println("无法复制解码器参数")
		return nil, nil, errors.New(fmt.Sprintf("无法复制解码器参数: %s", err))
	}

	//打开解码器
	if err := codecContext.Open(codec, nil); err != nil {
		codecContext.Free()
		return nil, nil, errors.New(fmt.Sprintf("无法打开解码器: %s", err))
	}
	return codecContext, codec, nil
}

// YUV420P像素格式转RGB
func YUV420PToRGB(yuvData []byte, width, height int) image.Image {
	// YUV420P格式中，Y分量的大小（按字节算）
	ySize := width * height
	// 计算U分量和V分量的大小（按字节算），它们是Y分量大小的四分之一
	uvSize := ySize / 4

	// 提取Y、U、V分量的数据切片
	yData := yuvData[:ySize]
	uData := yuvData[ySize : ySize+uvSize]
	vData := yuvData[ySize+uvSize:]

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// 计算当前像素在Y、U、V数据中的索引
			yIndex := y*width + x
			uIndex := (y/2)*(width/2) + (x / 2)
			vIndex := (y/2)*(width/2) + (x / 2)

			// 获取Y、U、V分量的值
			yVal := int(yData[yIndex])
			uVal := int(uData[uIndex]) - 128
			vVal := int(vData[vIndex]) - 128
			// 先进行浮点数运算
			rFloat := float64(yVal) + 1.402*float64(vVal)
			gFloat := float64(yVal) - 0.34414*float64(uVal) - 0.71414*float64(vVal)
			bFloat := float64(yVal) + 1.772*float64(uVal)

			// 将浮点数结果转换为整数类型
			r := int(rFloat + 0.5)
			g := int(gFloat + 0.5)
			b := int(bFloat + 0.5)

			// 限制RGB值在0-255范围内
			r = clamp(r, 0, 255)
			g = clamp(g, 0, 255)
			b = clamp(b, 0, 255)

			img.Set(x, y, color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255})
		}
	}

	return img
}

func clamp(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}
