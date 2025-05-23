package main

import (
	"bytes"
	"errors"
	"ffmpeg_video_capture/buffer"
	redis "ffmpeg_video_capture/redis_util"
	"fmt"
	"image/jpeg"
	"log"
	"math"
	"strings"
	"time"
)

var url = ""

var redisClient *redis.RedisClient

func init() {
	client, err := redis.DefaultClient()
	if err != nil {
		fmt.Println("初始化redis连接池失败")
		return
	}
	redisClient = client

}

func main() {
	astiav.SetLogLevel(astiav.LogLevelDebug)
	astiav.SetLogCallback(func(c astiav.Classer, l astiav.LogLevel, fmt, msg string) {
		var cs string
		if c != nil {
			if cl := c.Class(); cl != nil {
				cs = " - class: " + cl.String()
			}
		}
		log.Printf("ffmpeg log: %s%s - level: %d\n", strings.TrimSpace(msg), cs, l)
	})
	err := CaptureVideoAudioAndPushToRedis(url, "VideoData", "AudioData", "ImageData", 5)
	//err = CaptureVideoAudioAndPushToRedis(url, "VideoData", "AudioData", "ImageData", 5)
	if err != nil {
		log.Println(err)
	}

}

func CaptureVideoAudioAndPushToRedis(rtspUrl, videoKey, audioKey, imageKey string, seconds time.Duration) error {

	// 时长校验
	if seconds <= 0 {
		return errors.New("时长不能小于或等于0")
	}
	// 保存的视频会多出1秒
	seconds = seconds - 1
	outputDuration := seconds * time.Second

	// 设置参数
	options := &astiav.Dictionary{}
	options.Set("rtsp_transport", "tcp", astiav.DictionaryFlags(0)) //tcp传输
	options.Set("buffer_size", "8192", astiav.DictionaryFlags(0))   //缓冲区大小
	options.Set("max_delay", "5000", astiav.DictionaryFlags(0))     //最大处理延迟

	inputFormatCtx, err := ffmpegutil.GetInputFormatContext(rtspUrl, options)
	if err != nil {
		return err
	}
	defer inputFormatCtx.Free()
	defer options.Free()

	videoInputStream := ffmpegutil.FindStream(inputFormatCtx, astiav.MediaTypeVideo)
	audioInputStream := ffmpegutil.FindStream(inputFormatCtx, astiav.MediaTypeAudio)

	if audioInputStream == nil || videoInputStream == nil {
		return errors.New("未找到视频流或音频流")
	}

	log.Println("===========流索引信息===========")
	log.Println("视频流索引:", videoInputStream.Index())
	log.Println("音频流索引:", audioInputStream.Index())

	// 视频时长和帧率
	duration := inputFormatCtx.Duration()
	fps := videoInputStream.AvgFrameRate().Num() / videoInputStream.AvgFrameRate().Den()

	// rtsp流的duration为负数
	if duration < 0 {
		duration = math.MaxInt64
	}

	// 输出时长大于视频时长
	if outputDuration.Microseconds() > duration {
		outputDuration = time.Duration(duration * 1000)
	}

	// 分配mp4输出格式上下文
	mp4OutputFormatCtx, err := astiav.AllocOutputFormatContext(nil, "mp4", "")
	if err != nil || mp4OutputFormatCtx == nil {
		return errors.New(fmt.Sprintf("分配mp4输出格式上下文失败: %s", err))
	}
	defer mp4OutputFormatCtx.Free()

	// 分配wav输出格式上下文
	wavOutputFormatCtx, err := astiav.AllocOutputFormatContext(nil, "wav", "")
	if err != nil || wavOutputFormatCtx == nil {
		return errors.New(fmt.Sprintf("分配wav输出格式上下文失败: %s", err))
	}
	defer wavOutputFormatCtx.Free()

	// 获得视频解码器上下文，并打开解码器
	videoDecoderCtx, videoDecoder, err := ffmpegutil.FindAndOpenDecoderCtx(videoInputStream)
	if videoDecoderCtx == nil || err != nil {
		return err
	}
	defer videoDecoderCtx.Free()

	// 获得音频解码器上下文，并打开解码器
	audioDecoderCtx, audioDecoder, err := ffmpegutil.FindAndOpenDecoderCtx(audioInputStream)
	if err != nil || audioDecoderCtx == nil {
		return err
	}
	defer audioDecoderCtx.Free()

	//创建aac格式编码器
	aacEncoder := astiav.FindEncoder(astiav.CodecIDAac)
	aacEncoderCtx := astiav.AllocCodecContext(aacEncoder)
	defer aacEncoderCtx.Free()
	//设置编码器参数
	aacEncoderCtx.SetSampleFormat(astiav.SampleFormatFltp)
	aacEncoderCtx.SetChannelLayout(audioDecoderCtx.ChannelLayout())
	aacEncoderCtx.SetSampleRate(8000)
	aacEncoderCtx.SetBitRate(48000)

	//添加全局头信息
	aacEncoderCtx.SetFlags(aacEncoderCtx.Flags().Add(astiav.CodecContextFlagGlobalHeader))
	if err = aacEncoderCtx.Open(aacEncoder, nil); err != nil {
		return errors.New(fmt.Sprintf("无法打开aac编码器: %s", err))
	}

	// 视频宽高信息
	width := videoDecoderCtx.Width()
	height := videoDecoderCtx.Height()

	// 存放数据的缓冲区
	videoBuf := buffer.NewEmptyBuffer()
	audioBuf := buffer.NewEmptyBuffer()

	// 分配音频IO上下文
	wavIOContext, err := astiav.AllocIOContext(
		8192,
		true,
		nil,
		func(offset int64, whence int) (n int64, err error) {
			return audioBuf.Seek(offset, whence)
		},
		func(b []byte) (n int, err error) {
			return audioBuf.Write(b)
		},
	)
	if err != nil {
		return errors.New(fmt.Sprintf("分配wav音频IO上下文失败: %s", err))
	}
	defer wavIOContext.Free()

	// 音频IO上下文保存到输出格式上下文中
	wavOutputFormatCtx.SetPb(wavIOContext)

	//分配视频IO上下文
	mp4IOContext, err := astiav.AllocIOContext(
		8192,
		true,
		nil,
		func(offset int64, whence int) (n int64, err error) {
			return videoBuf.Seek(offset, whence)
		},
		func(b []byte) (n int, err error) {
			return videoBuf.Write(b)
		},
	)

	if err != nil {
		return errors.New(fmt.Sprintf("分配mp4视频IO上下文失败: %s", err))
	}
	defer mp4IOContext.Free()

	// 视频IO上下文保存到输出格式上下文中
	mp4OutputFormatCtx.SetPb(mp4IOContext)

	// 为mp4输出格式上下文创建视频输出流
	mp4VideoOutputStream, err := ffmpegutil.CreateStreamAndCopyParams(mp4OutputFormatCtx, videoInputStream)
	if err != nil {
		return errors.New(fmt.Sprintf("创建mp4视频输出流失败: %s", err))
	}

	// 为mp4输出格式上下文创建音频输出流
	mp4AudioOutputStream := mp4OutputFormatCtx.NewStream(nil)
	if err = mp4AudioOutputStream.CodecParameters().FromCodecContext(aacEncoderCtx); err != nil {
		return errors.New(fmt.Sprintf("创建mp4音频输出流失败,无法复制编码参数: %s", err))
	}
	mp4AudioOutputStream.CodecParameters().SetCodecTag(0)

	// 为wav输出格式上下文创建音频输出流
	wavAudioOutputStream, err := ffmpegutil.CreateStreamAndCopyParams(wavOutputFormatCtx, audioInputStream)
	if err != nil {
		return errors.New(fmt.Sprintf("创建wav音频输出流失败: %s", err))
	}

	// 打印视频信息
	log.Println("===========视频流信息===========")
	log.Printf("视频形式：%s", inputFormatCtx.InputFormat().Name())
	log.Printf("视频时长：%.2f s", float64(duration)/float64(astiav.TimeBase))
	log.Printf("视频fps：%d", fps)
	log.Printf("视频宽高：%d,%d", width, height)
	log.Printf("视频像素格式：%s", videoDecoderCtx.PixelFormat().Name())
	log.Printf("视频解码器的名称：%s", videoDecoder.Name())

	// 打印音频信息
	log.Println("===========音频流信息===========")
	log.Printf("音频解码器：%s", audioDecoder.Name())
	log.Printf("音频采样格式：%s", audioDecoderCtx.SampleFormat().Name())
	log.Printf("码率： %d", audioDecoderCtx.BitRate())
	log.Printf("采样率： %d", audioDecoderCtx.SampleRate())

	// 写入MP4文件头
	if err = mp4OutputFormatCtx.WriteHeader(nil); err != nil {
		return errors.New(fmt.Sprintf("写入MP4文件头失败: %s", err))
	}

	// 写入WAV文件头
	if err = wavOutputFormatCtx.WriteHeader(nil); err != nil {
		return errors.New(fmt.Sprintf("写入WAV文件头失败: %s", err))
	}

	// 分配packet和frame
	packet := astiav.AllocPacket()
	defer packet.Free()
	audioFrame := astiav.AllocFrame()
	audioFrame.SetSampleFormat(audioDecoderCtx.SampleFormat())
	audioFrame.SetChannelLayout(audioDecoderCtx.ChannelLayout())
	audioFrame.SetSampleRate(audioDecoderCtx.SampleRate())
	defer audioFrame.Free()
	resampledFrame := astiav.AllocFrame()
	defer resampledFrame.Free()
	resampledFrame.SetSampleFormat(aacEncoderCtx.SampleFormat())
	resampledFrame.SetChannelLayout(aacEncoderCtx.ChannelLayout())
	resampledFrame.SetSampleRate(aacEncoderCtx.SampleRate())

	//分配重采样上下文
	swrCtx := astiav.AllocSoftwareResampleContext()
	defer swrCtx.Free()
	swrCtx.SetInt("in_sample_rate", 8000, 0)
	swrCtx.SetInt("out_sample_rate", 8000, 0)
	swrCtx.SetChannelLayout("in_channel_layout", astiav.ChannelLayoutMono, 0)
	swrCtx.SetChannelLayout("out_channel_layout", astiav.ChannelLayoutMono, 0)
	swrCtx.SetSampleFormat("in_sample_fmt", audioDecoderCtx.SampleFormat(), 0)
	swrCtx.SetSampleFormat("out_sample_fmt", aacEncoderCtx.SampleFormat(), 0)
	swrCtx.Init()
	// 读取和写入数据
	var imageBytes []byte
	saveFrame := true
	startTime := time.Now()

	for time.Since(startTime) < outputDuration {
		// 读帧
		if err = inputFormatCtx.ReadFrame(packet); err != nil {
			// 读到文件尾
			if errors.Is(err, astiav.ErrEof) {
				break
			} else {
				return errors.New(fmt.Sprintf("读取数据帧失败: %s", err))
			}
		}
		if packet.StreamIndex() == videoInputStream.Index() {
			// 单独处理视频流
			// 保存一帧图片
			if saveFrame {
				videoFrame := astiav.AllocFrame()
				videoPacket := packet.Clone()
				// 发送给解码器
				err = videoDecoderCtx.SendPacket(videoPacket)
				if err != nil {
					return errors.New(fmt.Sprintf("视频数据发送给视频解码器失败:%s", err))
				}
				// 接收解码后的数据
				err = videoDecoderCtx.ReceiveFrame(videoFrame)
				if err != nil {
					return errors.New(fmt.Sprintf("从视频解码器获取视频帧失败:%s", err))
				}

				// 视频帧缓冲区大小
				size, _ := videoFrame.ImageBufferSize(1)
				imageBuf := make([]byte, size)

				// 数据拷贝到缓冲区
				_, err = videoFrame.ImageCopyToBuffer(imageBuf, 1)
				if err != nil {
					return errors.New(fmt.Sprintf("图像数据拷贝到缓冲区中失败:%s", err))
				}

				// YUV转RGB
				img := ffmpegutil.YUV420PToRGB(imageBuf, width, height)
				// 数据编码成jpg格式（压缩）
				var encodedBuffer bytes.Buffer
				err = jpeg.Encode(&encodedBuffer, img, &jpeg.Options{Quality: jpeg.DefaultQuality})
				if err != nil {
					return errors.New(fmt.Sprintf("图像编码失败:%s", err))
				}
				// jpg编码后的字节数据
				imageBytes = encodedBuffer.Bytes()
				videoFrame.Free()
				saveFrame = false
				videoPacket.Free()
			}
			// 更新数据帧参数
			packet.SetStreamIndex(mp4VideoOutputStream.Index())
			// 时间戳尺度变换
			packet.RescaleTs(videoInputStream.TimeBase(), mp4VideoOutputStream.TimeBase())
			packet.SetPos(-1)

			// 交叉写入视频输出缓冲区
			if err = mp4OutputFormatCtx.WriteInterleavedFrame(packet); err != nil {
				return errors.New(fmt.Sprintf("交叉写入mp4数据帧失败: %s", err))
			}
			packet.Unref()
		} else if packet.StreamIndex() == audioInputStream.Index() {
			audioPacket := packet.Clone()
			//单独处理音频流
			//音频数据发送给解码器
			err = audioDecoderCtx.SendPacket(packet)
			if err != nil {
				return errors.New(fmt.Sprintf("发送数据包到解码器失败：%s", err))
			}
			for {
				err = audioDecoderCtx.ReceiveFrame(resampledFrame)
				if err != nil {
					if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
						break
					} else {
						return errors.New(fmt.Sprintf("接收解码帧失败：%s", err))
					}
				}

				//重采样音频帧
				err = swrCtx.ConvertFrame(audioFrame, resampledFrame)
				if err != nil {
					return errors.New(fmt.Sprintf("音频重采样失败：%s", err))
				}

				err = aacEncoderCtx.SendFrame(resampledFrame)
				if err != nil {
					return errors.New(fmt.Sprintf("发送数据包到aac编码器失败：%s", err))
				}
				for {
					err = aacEncoderCtx.ReceivePacket(packet)
					if err != nil {
						if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
							break
						} else {
							return errors.New(fmt.Sprintf("接收aac编码包失败：%s", err))
						}
					}

					// 音频帧写入mp4文件
					// 更新数据帧参数
					packet.SetStreamIndex(mp4AudioOutputStream.Index())
					// 时间戳尺度变换
					packet.RescaleTs(audioInputStream.TimeBase(), mp4AudioOutputStream.TimeBase())
					packet.SetPos(-1)

					// 交叉写入视频输出缓冲区
					if err = mp4OutputFormatCtx.WriteInterleavedFrame(packet); err != nil {
						return errors.New(fmt.Sprintf("交叉写入mp4数据帧失败: %s", err))
					}
					packet.Unref()
				}
				audioFrame.Unref()
				resampledFrame.Unref()

			}

			//// 音频帧写入mp4文件
			//// 更新数据帧参数
			//packet.SetStreamIndex(mp4AudioOutputStream.Index())
			//// 时间戳尺度变换
			//packet.RescaleTs(audioInputStream.TimeBase(), mp4AudioOutputStream.TimeBase())
			//packet.SetPos(-1)
			//
			//// 交叉写入视频输出缓冲区
			//if err = mp4OutputFormatCtx.WriteInterleavedFrame(packet); err != nil {
			//	return errors.New(fmt.Sprintf("交叉写入mp4数据帧失败: %s", err))
			//}
			//packet.Unref()

			// 音频数据写入wav文件
			// 更新数据帧参数
			audioPacket.SetStreamIndex(wavAudioOutputStream.Index())
			audioPacket.RescaleTs(audioInputStream.TimeBase(), wavAudioOutputStream.TimeBase())
			audioPacket.SetPos(-1)

			// 交叉写入音频输出缓冲区
			if err = wavOutputFormatCtx.WriteInterleavedFrame(audioPacket); err != nil {
				return errors.New(fmt.Sprintf("交叉写入音频帧失败: %s", err))
			}
			audioPacket.Free()
		}

	}

	// 写入MP4文件尾
	if err = mp4OutputFormatCtx.WriteTrailer(); err != nil {
		return errors.New(fmt.Sprintf("写入MP4文件尾失败: %s", err))
	}

	// 写入WAV文件尾
	if err = wavOutputFormatCtx.WriteTrailer(); err != nil {
		return errors.New(fmt.Sprintf("写入WAV文件尾失败: %s", err))
	}

	// 将音频字节数据存入redis
	audioBytes := audioBuf.Bytes()
	err = redisClient.Push(audioKey, audioBytes)
	if err != nil {
		return errors.New(fmt.Sprintf("音频数据推送redis失败: %s", err))
	} else {
		log.Println("音频数据推送redis成功")
	}

	// 将视频字节数据存入redis
	videoBytes := videoBuf.Bytes()
	err = redisClient.Push(videoKey, videoBytes)
	if err != nil {
		return errors.New(fmt.Sprintf("视频数据推送redis失败: %s", err))
	} else {
		log.Println("视频数据推送redis成功")
	}

	// 将图片字节数据存入redis
	err = redisClient.Push(imageKey, imageBytes)
	if err != nil {
		return errors.New(fmt.Sprintf("图片数据推送redis失败: %s", err))
	} else {
		log.Println("图片数据推送redis成功")
	}
	return nil
}
