package main

import (
	"bytes"
	"errors"
	"ffmpeg_video_capture/buffer"
	ffmpegutil "ffmpeg_video_capture/ffmpeg_util"
	redis "ffmpeg_video_capture/redis_util"
	"fmt"
	"image/jpeg"
	"log"
	"math"
	"time"
)

var url = ""
var videoKey = "VideoData"
var imageKey = "ImageData"
var audioKey = "AudioData"

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
	CaptureVideoAndPushToRedis(url, videoKey, audioKey, imageKey, 5)
	CaptureVideoAndPushToRedis(url, videoKey, audioKey, imageKey, 5)
}

func CaptureVideoAndPushToRedis(rtspUrl string, videoKey string, audioKey string, imageKey string, seconds time.Duration) error {
	//时长校验
	if seconds <= 0 {
		return errors.New("时长不能小于0")
	}
	//保存的视频会多出1秒，这里减1
	seconds = seconds - 1
	outputDuration := seconds * time.Second

	// 设置参数
	options := &astiav.Dictionary{}
	_ = options.Set("rtsp_transport", "tcp", astiav.DictionaryFlags(0)) //tcp传输
	_ = options.Set("buffer_size", "8192", astiav.DictionaryFlags(0))   //缓冲区大小
	_ = options.Set("max_delay", "5000", astiav.DictionaryFlags(0))     //最大处理延迟

	inputFormatCtx, err := ffmpegutil.GetInputFormatContext(rtspUrl, options)
	if err != nil {
		return err
	}
	defer inputFormatCtx.Free()
	defer options.Free()

	var videoInputStream, audioInputStream *astiav.Stream
	videoInputStream = ffmpegutil.FindStream(inputFormatCtx, astiav.MediaTypeVideo)
	audioInputStream = ffmpegutil.FindStream(inputFormatCtx, astiav.MediaTypeAudio)

	if audioInputStream == nil || videoInputStream == nil {
		log.Fatal("未找到视频流或音频流")
	}

	log.Println("===========流索引信息===========")
	log.Println("视频流索引:", videoInputStream.Index())
	log.Println("音频流索引:", audioInputStream.Index())

	// 获得视频解码器上下文，并打开解码器
	videoDecoderCtx, videoDecoder, err := ffmpegutil.FindAndOpenDecoderCtx(videoInputStream)
	if videoDecoderCtx == nil || err != nil {
		return err
	}
	defer videoDecoderCtx.Free()

	// 获得音频解码器上下文，并打开解码器
	audioDecoderCtx, audioDecoder, err := ffmpegutil.FindAndOpenDecoderCtx(audioInputStream)
	if audioDecoderCtx == nil || err != nil {
		return err
	}
	defer audioDecoderCtx.Free()

	//视频信息
	width := videoDecoderCtx.Width()
	height := videoDecoderCtx.Height()
	duration := inputFormatCtx.Duration()
	fps := videoInputStream.AvgFrameRate().Num() / videoInputStream.AvgFrameRate().Den()

	//rtsp流，duration为负数
	if duration < 0 {
		duration = math.MaxInt64
	}

	//输出时长大于视频时长
	if outputDuration.Microseconds() > duration {
		outputDuration = time.Duration(duration * 1000)
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

	// 分配mp4视频输出格式上下文
	mp4OutputFormatCtx, err := astiav.AllocOutputFormatContext(nil, "mp4", "")
	if err != nil || mp4OutputFormatCtx == nil {
		return errors.New(fmt.Sprintf("分配mp4输出格式上下文失败: %s", err))
	}
	defer mp4OutputFormatCtx.Free()

	// 分配wav音频输出格式上下文
	wavOutputFormatCtx, err := astiav.AllocOutputFormatContext(nil, "wav", "")
	if err != nil || wavOutputFormatCtx == nil {
		return errors.New(fmt.Sprintf("分配wav输出格式上下文失败: %s", err))
	}
	defer wavOutputFormatCtx.Free()

	// 存放mp4数据的缓冲区
	videoBuf := buffer.NewEmptyBuffer()

	//分配视频IO上下文
	mp4IOContext, err := astiav.AllocIOContext(
		4096,
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

	// mp4视频IO上下文保存到输出格式上下文中
	mp4OutputFormatCtx.SetPb(mp4IOContext)

	// 存放wav数据的缓冲区
	audioBuf := buffer.NewEmptyBuffer()

	// 分配音频IO上下文
	wavIOContext, err := astiav.AllocIOContext(
		4096,
		true,
		nil,
		func(offset int64, whence int) (n int64, err error) { return audioBuf.Seek(offset, whence) },
		func(b []byte) (n int, err error) { return audioBuf.Write(b) },
	)
	if err != nil {
		return errors.New(fmt.Sprintf("分配wav音频IO上下文失败: %s", err))
	}
	defer wavIOContext.Free()

	// wav音频IO上下文保存到输出格式上下文中
	wavOutputFormatCtx.SetPb(wavIOContext)

	//创建aac编码器上下文
	mp4AudioEncoder := astiav.FindEncoder(astiav.CodecIDAac)
	mp4AudioEncoderCtx := astiav.AllocCodecContext(mp4AudioEncoder)
	defer mp4AudioEncoderCtx.Free()
	mp4AudioEncoderCtx.SetSampleRate(audioDecoderCtx.SampleRate())
	mp4AudioEncoderCtx.SetChannelLayout(audioDecoderCtx.ChannelLayout())
	mp4AudioEncoderCtx.SetBitRate(48000)
	mp4AudioEncoderCtx.SetSampleFormat(astiav.SampleFormatFltp)

	if err = mp4AudioEncoderCtx.Open(mp4AudioEncoder, nil); err != nil {
		return errors.New(fmt.Sprintf("无法打开aac编码器: %s", err))
	}

	//创建mp4视频输出流
	mp4VideoOutputStream, err := ffmpegutil.CreateStreamAndCopyParams(mp4OutputFormatCtx, videoInputStream)
	if err != nil {
		return errors.New(fmt.Sprintf("创建mp4视频输出流失败: %s", err))
	}
	mp4VideoOutputStream.CodecParameters().SetCodecTag(0)

	//创建mp4音频输出流
	mp4AudioOutputStream := mp4OutputFormatCtx.NewStream(nil)
	err = mp4AudioEncoderCtx.ToCodecParameters(mp4AudioOutputStream.CodecParameters())
	if err != nil {
		return errors.New(fmt.Sprintf("创建mp4音频输出流失败,无法复制编码参数: %s", err))
	}
	mp4AudioOutputStream.CodecParameters().SetCodecTag(0)

	// 创建wav音频输出流
	wavAudioOutputStream, err := ffmpegutil.CreateStreamAndCopyParams(wavOutputFormatCtx, audioInputStream)
	if err != nil {
		return errors.New(fmt.Sprintf("创建wav音频输出流失败: %s", err))
	}

	// 分配packet
	packet := astiav.AllocPacket()
	defer packet.Free()

	//分配解码帧
	decodedFrame := astiav.AllocFrame()
	defer decodedFrame.Free()

	//存储图片数据
	var imageBytes []byte

	// 分配重采样上下文
	swrCtx := astiav.AllocSoftwareResampleContext()
	defer swrCtx.Free()

	// 分配重采样帧
	resampledFrame := astiav.AllocFrame()
	defer resampledFrame.Free()
	//设置重采样帧参数
	resampledFrame.SetChannelLayout(mp4AudioEncoderCtx.ChannelLayout())
	resampledFrame.SetSampleFormat(mp4AudioEncoderCtx.SampleFormat())
	resampledFrame.SetSampleRate(mp4AudioEncoderCtx.SampleRate())
	resampledFrame.SetNbSamples(1024)

	//最终音频帧
	finalFrame := astiav.AllocFrame()
	defer finalFrame.Free()
	//设置最终音频帧参数
	finalFrame.SetChannelLayout(resampledFrame.ChannelLayout())
	finalFrame.SetNbSamples(resampledFrame.NbSamples())
	finalFrame.SetSampleFormat(resampledFrame.SampleFormat())
	finalFrame.SetSampleRate(resampledFrame.SampleRate())

	//写入MP4文件头
	if err = mp4OutputFormatCtx.WriteHeader(nil); err != nil {
		log.Fatal(fmt.Errorf("写入MP4文件头失败: %w", err))
	}

	//写入WAV文件头
	if err = wavOutputFormatCtx.WriteHeader(nil); err != nil {
		return errors.New(fmt.Sprintf("写入WAV文件头失败: %s", err))
	}

	if err = finalFrame.AllocBuffer(0); err != nil {
		return errors.New(fmt.Sprintf("分配缓冲区失败: %s", err))
	}
	if err = finalFrame.AllocSamples(0); err != nil {
		return errors.New(fmt.Sprintf("分配样本失败: %s", err))
	}

	//分配音频队列
	audioFifo := astiav.AllocAudioFifo(finalFrame.SampleFormat(), finalFrame.ChannelLayout().Channels(), finalFrame.NbSamples())
	defer audioFifo.Free()

	firstFrame := true
	startTime := time.Now()
	for time.Since(startTime) < outputDuration {
		// 使用闭包简化解引用
		// 读帧
		if err = inputFormatCtx.ReadFrame(packet); err != nil {
			if errors.Is(err, astiav.ErrEof) {
				break
			}
			return errors.New(fmt.Sprintf("读取数据帧失败: %s", err))
		}
		// 解引用
		if packet.StreamIndex() == videoInputStream.Index() {
			//单独处理视频流
			//保存一帧图片
			if firstFrame {
				//发送给解码器
				err = videoDecoderCtx.SendPacket(packet)
				if err != nil {
					return errors.New(fmt.Sprintf("视频数据发送给视频解码器失败: %s", err))
				}
				//接收解码后的数据
				err = videoDecoderCtx.ReceiveFrame(decodedFrame)
				if err != nil {
					return errors.New(fmt.Sprintf("从视频解码器获取视频帧失败: %s", err))
				}
				//视频帧缓冲区大小
				size, _ := decodedFrame.ImageBufferSize(1)
				imageBuf := make([]byte, size)

				//数据拷贝到缓冲区
				_, err = decodedFrame.ImageCopyToBuffer(imageBuf, 1)
				if err != nil {
					return errors.New(fmt.Sprintf("图像数据拷贝到缓冲区中失败: %s", err))
				}
				//YUV转RGB
				img := ffmpegutil.YUV420PToRGB(imageBuf, width, height)

				//数据编码成jpg格式（压缩）
				var encodedBuffer bytes.Buffer
				err = jpeg.Encode(&encodedBuffer, img, &jpeg.Options{Quality: jpeg.DefaultQuality})
				if err != nil {
					return errors.New(fmt.Sprintf("图像编码失败: %s", err))
				}
				//jpg编码后的字节数据
				imageBytes = encodedBuffer.Bytes()

				decodedFrame.Unref()
				firstFrame = false
			}
			//写入视频帧
			// 更新数据帧参数
			packet.SetStreamIndex(mp4VideoOutputStream.Index())
			packet.RescaleTs(videoInputStream.TimeBase(), mp4VideoOutputStream.TimeBase())
			packet.SetPos(-1)
			// 交叉写入输出缓冲区
			if err = mp4OutputFormatCtx.WriteInterleavedFrame(packet); err != nil {
				return errors.New(fmt.Sprintf("交叉写入视频帧失败: %s", err))
			}
		} else if packet.StreamIndex() == audioInputStream.Index() {
			//单独处理音频流，音频流做两个处理，wav的音频流直接写入，mp4音频流转码成aac格式再写入
			//直接写入wav文件
			audioPacket := packet.Clone()
			// 更新数据帧参数
			audioPacket.SetStreamIndex(wavAudioOutputStream.Index())
			audioPacket.RescaleTs(audioInputStream.TimeBase(), wavAudioOutputStream.TimeBase())
			audioPacket.SetPos(-1)
			// 交叉写入输出缓冲区
			if err = wavOutputFormatCtx.WriteFrame(audioPacket); err != nil {
				return errors.New(fmt.Sprintf("交叉写入音频帧失败: %s", err))
			}
			audioPacket.Free()

			//解码音频帧
			err = audioDecoderCtx.SendPacket(packet)
			if err != nil {
				return errors.New(fmt.Sprintf("视频数据发送给音频解码器失败: %s", err))
			}
			for {
				if err = audioDecoderCtx.ReceiveFrame(decodedFrame); err != nil {
					if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
						break
					}
					return errors.New(fmt.Sprintf("从音频解码器中获取解码帧失败: %s", err))
				}

				//重采样音频帧
				if err = swrCtx.ConvertFrame(decodedFrame, resampledFrame); err != nil {
					return errors.New(fmt.Sprintf("重采样音频帧失败: %s", err))
				}
				// 将重采样后的音频帧添加到音频队列中
				if err = addResampledFrameToAudioFIFO(false, mp4AudioEncoderCtx, mp4OutputFormatCtx, audioFifo, resampledFrame, finalFrame, audioInputStream, mp4AudioOutputStream); err != nil {
					return errors.New(fmt.Sprintf("添加重采样后的音频帧到音频队列中失败: %s", err))
				}

				// 刷新重采样上下文
				if err = flushSoftwareResampleContext(false, mp4AudioEncoderCtx, mp4OutputFormatCtx, audioFifo, swrCtx, resampledFrame, finalFrame, audioInputStream, mp4AudioOutputStream); err != nil {
					return errors.New(fmt.Sprintf("刷新重采样上下文失败: %s", err))
				}
				decodedFrame.Unref()
			}
		}
		packet.Unref()
	}

	//写入MP4文件尾
	if err = mp4OutputFormatCtx.WriteTrailer(); err != nil {
		return errors.New(fmt.Sprintf("写入MP4文件尾失败: %s", err))
	}

	//写入WAV文件尾
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

func flushSoftwareResampleContext(finalFlush bool, mp4AudioEncoderCtx *astiav.CodecContext, mp4OutputFormatCtx *astiav.FormatContext, audioFifo *astiav.AudioFifo, swrCtx *astiav.SoftwareResampleContext, resampledFrame *astiav.Frame, finalFrame *astiav.Frame, inputStream, outputStream *astiav.Stream) error {
	for {
		if finalFlush || swrCtx.Delay(int64(resampledFrame.SampleRate())) >= int64(resampledFrame.NbSamples()) {
			// 刷新重采样器
			if err := swrCtx.ConvertFrame(nil, resampledFrame); err != nil {
				return errors.New(fmt.Sprintf("刷新重采样器失败: %s", err))
			}
			// 添加重采样帧到音频队列中
			if err := addResampledFrameToAudioFIFO(finalFlush, mp4AudioEncoderCtx, mp4OutputFormatCtx, audioFifo, resampledFrame, finalFrame, inputStream, outputStream); err != nil {
				return errors.New(fmt.Sprintf("添加重采样帧到音频队列中失败: %s", err))
			}

			if finalFlush && resampledFrame.NbSamples() == 0 {
				break
			}
			continue
		}
		break
	}
	return nil
}

func addResampledFrameToAudioFIFO(flush bool, mp4AudioEncoderCtx *astiav.CodecContext, mp4OutputFormatCtx *astiav.FormatContext, audioFifo *astiav.AudioFifo, resampledFrame *astiav.Frame, finalFrame *astiav.Frame, inputStream, outputStream *astiav.Stream) error {
	// 写入音频队列
	if resampledFrame.NbSamples() > 0 {
		if _, err := audioFifo.Write(resampledFrame); err != nil {
			return fmt.Errorf("写入音频队列失败: %w", err)
		}
	}
	outputPacket := astiav.AllocPacket()
	defer outputPacket.Free()
	for {
		if (flush && audioFifo.Size() > 0) || (!flush && audioFifo.Size() >= finalFrame.NbSamples()) {
			nbSamples, err := audioFifo.Read(finalFrame)
			//执行编码，写入操作
			//设置时间戳
			finalFrame.SetNbSamples(nbSamples)
			err = mp4AudioEncoderCtx.SendFrame(finalFrame)
			if err != nil {
				return errors.New(fmt.Sprintf("数据发送给输出音频编码器失败: %s", err))
			}
			err = mp4AudioEncoderCtx.ReceivePacket(outputPacket)
			outputPacket.RescaleTs(inputStream.TimeBase(), outputStream.TimeBase())
			outputPacket.SetStreamIndex(outputStream.Index())
			outputPacket.SetPos(-1)
			if err != nil {
				if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
					break
				}
				return errors.New(fmt.Sprintf("从音频编码器中获取数据包失败: %s", err))
			}
			if err = mp4OutputFormatCtx.WriteInterleavedFrame(outputPacket); err != nil {
				return errors.New(fmt.Sprintf("交叉写入音频帧到mp4文件中失败: %s", err))
			}
			outputPacket.Unref()
			continue
		}
		break
	}
	return nil
}
