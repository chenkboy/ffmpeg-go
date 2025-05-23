package main

/*
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/avutil.h>
#include <libavutil/imgutils.h>
#include <libswresample/swresample.h>
#cgo pkg-config: libavformat libavutil libavcodec libswscale libswresample
*/
import "C"

import (
	"bytes"
	"errors"
	redis "ffmpeg_video_capture/redis_util"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"unsafe"
)

//var RTSP_URL = ""

var RTSP_URL = "test.mp4"
var redisHost = "127.0.0.1:6379"
var redisClient *redis.RedisClient
var videoOutput = "output.mp4"
var audioOutput = "output.wav"

var fps = 25
var duration = 2

func init() {
	client, err := redis.NewClient(&redis.RedisConf{
		Host:        redisHost,
		MaxIdle:     30,
		MaxActive:   60,
		IdleTimeout: 30,
		Password:    "Abc@1234",
		Db:          0,
	})
	if err != nil {
		fmt.Println("初始化redis连接池失败")
		return
	}
	redisClient = client
	// 初始化FFmpeg的网络组件
	C.avformat_network_init()
}

func main() {

	var options *C.AVDictionary

	// 调整参数，设置更大的缓冲区和最大延迟
	C.av_dict_set(&options, C.CString("buffer_size"), C.CString("2097152"), 0) // 2MB
	C.av_dict_set(&options, C.CString("max_delay"), C.CString("5000000"), 0)   // 500ms
	C.av_dict_set(&options, C.CString("rtsp_transport"), C.CString("tcp"), 0)  // 使用 TCP
	formatCtx, err := OpenRtspStream(RTSP_URL, options)

	if err != nil {
		return
	}
	defer C.avformat_close_input(&formatCtx)

	//所有转换成可索引的类型
	streams := (*[10]*C.struct_AVStream)(unsafe.Pointer(formatCtx.streams))
	// 找到视频流
	videoStreamIndex := FindStreamIndex(formatCtx, C.AVMEDIA_TYPE_VIDEO)
	if videoStreamIndex == -1 {
		fmt.Println("could not find video stream")
		return
	}
	C.swr_alloc()
	fmt.Println("video stream index:", videoStreamIndex)
	audioStreamIndex := FindStreamIndex(formatCtx, C.AVMEDIA_TYPE_AUDIO)
	if audioStreamIndex == -1 {
		fmt.Println("could not find audio stream")
	}
	fmt.Println("audio stream index:", audioStreamIndex)

	//视频解码器
	videoCodecCtx, videoCodec, err := FindAndOpenCodec(videoStreamIndex, formatCtx)
	defer C.avcodec_free_context(&videoCodecCtx)
	//音频解码器
	audioCodecCtx, audioCodec, err := FindAndOpenCodec(audioStreamIndex, formatCtx)
	defer C.avcodec_free_context(&audioCodecCtx)

	width := int(videoCodecCtx.width)
	height := int(videoCodecCtx.height)

	//videoOutputFormatCtx, err := AllocOutputFormatContext(nil, "mp4", videoOutput)
	//if err != nil {
	//	return
	//}
	//defer C.avformat_free_context(videoOutputFormatCtx)
	//
	//videoOutputStream := C.avformat_new_stream(videoOutputFormatCtx, videoCodec)
	//if ret := C.avcodec_parameters_copy(videoOutputStream.codecpar, streams[videoStreamIndex].codecpar); ret < 0 {
	//	log.Fatal("复制视频输入流编码参数到视频输出流中失败")
	//}
	//videoOutputStream.codecpar.codec_tag = C.uint(0)
	//
	//cVideoOutputFileName := C.CString(videoOutput)
	//defer C.free(unsafe.Pointer(cVideoOutputFileName))
	//
	//if C.avio_open(&videoOutputFormatCtx.pb, cVideoOutputFileName, C.AVIO_FLAG_WRITE) < 0 {
	//	fmt.Printf("Could not open output file\n")
	//	return
	//}
	//
	//if ret := C.avformat_write_header(videoOutputFormatCtx, nil); ret < 0 {
	//	log.Fatal("写入MP4文件头失败")
	//}

	//打印视频信息
	fmt.Println("视频的文件格式：", C.GoString(formatCtx.iformat.name))
	fmt.Println("视频时长：", float64(formatCtx.duration)/1000000)
	fmt.Printf("视频的宽高：%d,%d", width, height)
	fmt.Println()
	fmt.Println("视频解码器的名称：", C.GoString(videoCodec.name))
	fmt.Println("视频像素格式：", C.GoString(C.av_get_pix_fmt_name((C.enum_AVPixelFormat)(videoCodecCtx.pix_fmt))))

	//打印音频信息
	fmt.Println("音频解码器的名称：", C.GoString(audioCodec.long_name))
	fmt.Println("音频采样格式：", C.GoString(C.av_get_sample_fmt_name((C.enum_AVSampleFormat)(audioCodecCtx.sample_fmt))))

	var videoData []byte

	//分配存储压缩数据的packet(H264编码)
	packet := C.av_packet_alloc()
	defer C.av_packet_free(&packet)
	//分配存储解码后的数据的frame(YUV格式)
	frame := C.av_frame_alloc()
	defer C.av_frame_free(&frame)

	videoFrameCount := 0
	targetFrameCount := fps * duration

	var videoDuration float64
	targetDuration := float64(duration)

	for videoDuration < targetDuration && videoFrameCount < targetFrameCount {
		if C.av_read_frame(formatCtx, packet) < 0 {
			break
		}

		if packet.stream_index == videoStreamIndex {
			//发送视频帧到编码器
			if C.avcodec_send_packet(videoCodecCtx, packet) < 0 {
				fmt.Println("将视频packet发送给编码器失败")
				continue
			}

			//从编码器接收视频帧
			if C.avcodec_receive_frame(videoCodecCtx, frame) < 0 {
				fmt.Println("从编码器接收视频帧失败")
				continue
			}

			videoFrameCount++
			//视频帧数据大小
			size := int(C.av_image_get_buffer_size(videoCodecCtx.pix_fmt, C.int(videoCodecCtx.width), C.int(videoCodecCtx.height), 1))

			//存储解码后的原始帧数据
			buffer := make([]byte, size)
			data := (**C.uint8_t)(unsafe.Pointer(&frame.data[0]))
			lineSize := (*C.int)(unsafe.Pointer(&frame.linesize[0]))

			//将视频帧数据copy到指定缓冲区中
			C.av_image_copy_to_buffer((*C.uint8_t)(unsafe.Pointer(&buffer[0])), C.int(size), data, lineSize, videoCodecCtx.pix_fmt, C.int(videoCodecCtx.width), C.int(videoCodecCtx.height), 1)

			//将每一帧数据转换成RGB格式
			img, _ := YUV420PToRGB(buffer, width, height)
			fileName := fmt.Sprintf("image\\output%03d.jpg", videoFrameCount)

			//数据编码成jpg格式（压缩）
			var encodedBuffer bytes.Buffer
			err = jpeg.Encode(&encodedBuffer, img, &jpeg.Options{Quality: jpeg.DefaultQuality})
			if err != nil {
				fmt.Println("图像编码失败")
			}

			//jpg编码后的字节数据
			encodedBytes := encodedBuffer.Bytes()

			//保存图片
			err = SaveImage(encodedBytes, fileName)
			if err != nil {
				fmt.Printf("保存文件失败，err：%s ，文件名：%s", err, fileName)
				fmt.Println()
			} else {
				fmt.Printf("文件保存成功，文件名：%s", fileName)
				fmt.Println()
			}

			//将压缩后的帧数据追加到字节切片
			videoData = append(videoData, encodedBytes...)

			videoDuration += float64(frame.duration) * float64(C.av_q2d(streams[videoStreamIndex].time_base))
		} else if packet.stream_index == audioStreamIndex {
			//处理音频帧
			if C.avcodec_send_packet(audioCodecCtx, packet) < 0 {
				fmt.Println("将音频packet发送给编码器失败")
				continue
			}
			if C.avcodec_receive_frame(audioCodecCtx, frame) < 0 {
				fmt.Println("从编码器接收音频帧失败")
				continue
			}
		}

		//解引用
		C.av_packet_unref(packet)
		C.av_frame_unref(frame)
	}

	if err != nil {
		fmt.Println("读取数据失败")
	} else {
		fmt.Println("读取成功,数据长度", len(videoData))
	}

}

// AllocOutputFormatContext 分配输出上下文
func AllocOutputFormatContext(outputFormat *C.AVOutputFormat, formatName, filename string) (*C.AVFormatContext, error) {
	formatNameC := (*C.char)(nil)
	if len(formatName) > 0 {
		formatNameC = C.CString(formatName)
		defer C.free(unsafe.Pointer(formatNameC))
	}
	fileNameC := (*C.char)(nil)
	if len(filename) > 0 {
		fileNameC = C.CString(filename)
		defer C.free(unsafe.Pointer(fileNameC))
	}

	var formatCtx *C.AVFormatContext
	if ret := C.avformat_alloc_output_context2(&formatCtx, nil, formatNameC, fileNameC); ret < 0 {
		return nil, errors.New("分配输出上下文失败")
	}
	return formatCtx, nil
}

// OpenRtspStream 打开流并查找流信息
func OpenRtspStream(rtspURL string, options *C.AVDictionary) (*C.AVFormatContext, error) {
	var formatCtx *C.AVFormatContext
	url := C.CString(rtspURL)
	defer C.free(unsafe.Pointer(url))
	// 打开RTSP流
	if ret := C.avformat_open_input(&formatCtx, url, nil, &options); ret < 0 {
		return nil, fmt.Errorf("无法打开流")
	}

	// 查找流信息
	if ret := C.avformat_find_stream_info(formatCtx, nil); ret < 0 {
		return nil, fmt.Errorf("找不到流信息")
	}
	return formatCtx, nil
}

// FindStreamIndex 查找流索引
func FindStreamIndex(formatCtx *C.AVFormatContext, codecType C.enum_AVMediaType) C.int {
	streams := (*[10]*C.struct_AVStream)(unsafe.Pointer(formatCtx.streams))
	for i := 0; i < int(formatCtx.nb_streams); i++ {
		if streams[i].codecpar.codec_type == codecType {
			return C.int(i)
		}
	}
	return C.int(-1)
}

// FindAndOpenCodec 配置编解码器
func FindAndOpenCodec(streamIndex C.int, formatCtx *C.AVFormatContext) (*C.AVCodecContext, *C.AVCodec, error) {
	// 获取流的编解码器参数
	streams := (*[10]*C.struct_AVStream)(unsafe.Pointer(formatCtx.streams))
	codecPar := streams[streamIndex].codecpar
	codec := C.avcodec_find_decoder(codecPar.codec_id)
	if codec == nil {
		return nil, nil, fmt.Errorf("找不到对应的解码器")
	}

	codecCtx := C.avcodec_alloc_context3(codec)
	if codecCtx == nil {
		return nil, nil, fmt.Errorf("无法分配编解码器上下文")
	}

	ret := C.avcodec_parameters_to_context(codecCtx, codecPar)
	if ret < 0 {
		C.avcodec_free_context(&codecCtx)
		return nil, nil, fmt.Errorf("无法复制编解码器参数到上下文")
	}

	ret = C.avcodec_open2(codecCtx, codec, nil)
	if ret < 0 {
		C.avcodec_free_context(&codecCtx)
		return nil, nil, fmt.Errorf("无法打开编解码器")
	}

	return codecCtx, codec, nil
}

func YUV420PToRGB(yuvData []byte, width, height int) (image.Image, []byte) {
	rgbData := make([]byte, width*height*3)
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

			rgbIndex := (y*width + x) * 3
			// 限制RGB值在0-255范围内
			r = clip(r, 0, 255)
			g = clip(g, 0, 255)
			b = clip(b, 0, 255)

			rgbData[rgbIndex] = byte(r)
			rgbData[rgbIndex+1] = byte(g)
			rgbData[rgbIndex+2] = byte(r)
			img.Set(x, y, color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255})
		}
	}

	return img, rgbData
}

func clip(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func SaveImage(bytes []byte, savePath string) error {
	file, err := os.Create(savePath)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(bytes)
	if err != nil {
		return err
	}
	return nil
}
