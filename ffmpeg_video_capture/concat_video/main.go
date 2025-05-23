package main

import (
	"errors"
	"ffmpeg_video_capture/buffer"
	ffmpegutil "ffmpeg_video_capture/ffmpeg_util"
	redis "ffmpeg_video_capture/redis_util"
	"fmt"
	"github.com/asticode/go-astiav"
	"log"
	"os"
)

var redisClient *redis.RedisClient

type InputContext struct {
	fmtCtx      *astiav.FormatContext
	videoStream *astiav.Stream
	audioStream *astiav.Stream
}

type PacketTs struct {
	pts int64
	dts int64
}

func init() {
	client, err := redis.DefaultClient()
	if err != nil {
		log.Println("初始化redis连接池失败")
		return
	}
	redisClient = client
}

func main() {
	ConcatVideos("VideoData", "output.mp4")
}

func ConcatVideos(videoKey, outputFileName string) error {
	//视频字节数据列表
	var videoBufferList []*buffer.Buffer
	dataList, err := redisClient.GetAllElements(videoKey)
	if err != nil {
		return errors.New(fmt.Sprintf("获取视频数据出错: %s", err))
	}
	if len(dataList) == 0 {
		return errors.New(fmt.Sprintf("无视频数据: %s", err))
	} else {
		log.Printf("获取到%d条视频数据", len(dataList))
	}

	for _, videoData := range dataList {
		videoBytes := videoData.([]byte)
		videoBuffer := buffer.NewBuffer(videoBytes)
		videoBufferList = append(videoBufferList, videoBuffer)
	}

	var inputCtxList []*InputContext
	for _, videoBuffer := range videoBufferList {
		inputCtx := &InputContext{}
		// 为列表中的每一个视频数据分配一个输入格式上下文
		inputFormatCtx := astiav.AllocFormatContext()
		//分配IO上下文
		var ioContext *astiav.IOContext
		ioContext, err = astiav.AllocIOContext(
			8192,
			false,
			func(b []byte) (n int, err error) {
				return videoBuffer.Read(b)
			},
			func(offset int64, whence int) (n int64, err error) {
				return videoBuffer.Seek(offset, whence)
			},
			nil,
		)
		if err != nil {
			return errors.New(fmt.Sprintf("分配io上下文失败: %s", err))
		}

		inputFormatCtx.SetPb(ioContext)
		if err = inputFormatCtx.OpenInput("", astiav.FindInputFormat("mp4"), nil); err != nil {
			return errors.New(fmt.Sprintf("打开输入流失败: %s", err))
		}
		err = inputFormatCtx.FindStreamInfo(nil)
		if err != nil {
			return errors.New(fmt.Sprintf("查找流信息失败: %s", err))
		}
		videoStream := ffmpegutil.FindStream(inputFormatCtx, astiav.MediaTypeVideo)
		audioStream := ffmpegutil.FindStream(inputFormatCtx, astiav.MediaTypeAudio)
		inputCtx.fmtCtx = inputFormatCtx
		inputCtx.videoStream = videoStream
		inputCtx.audioStream = audioStream
		inputCtxList = append(inputCtxList, inputCtx)
	}
	defer freeFormatContextList(inputCtxList)

	// 分配mp4输出格式上下文
	outputFormatCtx, err := astiav.AllocOutputFormatContext(nil, "mp4", "")
	if err != nil || outputFormatCtx == nil {
		return errors.New(fmt.Sprintf("分配mp4视频输出格式上下文失败: %s", err))
	}
	defer outputFormatCtx.Free()
	//存放拼接后的视频数据
	videoBuf := buffer.NewEmptyBuffer()
	ioContext, err := astiav.AllocIOContext(
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
		return errors.New(fmt.Sprintf("分配视频IO上下文失败: %s", err))
	}
	defer ioContext.Free()

	// 视频IO上下文保存到输出格式上下文中
	outputFormatCtx.SetPb(ioContext)

	var videoOutputStream, audioOutputStream *astiav.Stream
	//为输出格式上下文创建视频输出流
	if inputCtxList[0].videoStream != nil {
		videoOutputStream, err = ffmpegutil.CreateStreamAndCopyParams(outputFormatCtx, inputCtxList[0].videoStream)
		if err != nil {
			return errors.New(fmt.Sprintf("创建视频输出流失败: %s", err))
		}
	}

	//为输出格式上下文创建音频输出流
	if inputCtxList[0].audioStream != nil {
		audioOutputStream, err = ffmpegutil.CreateStreamAndCopyParams(outputFormatCtx, inputCtxList[0].audioStream)
		if err != nil {
			return errors.New(fmt.Sprintf("创建音频输出流失败: %s", err))
		}
	}

	//写入MP4文件头
	if err = outputFormatCtx.WriteHeader(nil); err != nil {
		return errors.New(fmt.Sprintf("写入MP4文件头失败: %s", err))
	}

	//分配packet和frame
	packet := astiav.AllocPacket()
	defer packet.Free()

	//计算每帧的时长
	//interval := float64(astiav.TimeBase) / inputCtxList[0].videoStream.AvgFrameRate().Float64()
	//videoFrameDuration := int64(math.Round(interval / (inputCtxList[0].videoStream.TimeBase().Float64() * float64(astiav.TimeBase))))

	//记录上一条视频最后一帧的时间戳
	lastVideoPacketTs := &PacketTs{0, 0}
	lastAudioPacketTs := &PacketTs{0, 0}
	for i, inputCtx := range inputCtxList {
		if inputCtx == nil {
			return errors.New(fmt.Sprintf("输入上下文为空: %s", err))
		}
		//记录当前视频的时间戳
		currVideoPacketTs := &PacketTs{0, 0}
		currAudioPacketTs := &PacketTs{0, 0}
		for {
			// 读帧
			if err = inputCtx.fmtCtx.ReadFrame(packet); err != nil {
				//读到文件尾
				if errors.Is(err, astiav.ErrEof) {
					lastVideoPacketTs = currVideoPacketTs
					lastAudioPacketTs = currAudioPacketTs
					break
				} else {
					return errors.New(fmt.Sprintf("读取数据帧失败: %s", err))
				}
			}

			var inputStream, outputStream *astiav.Stream
			if packet.StreamIndex() == inputCtx.videoStream.Index() {
				inputStream = inputCtx.videoStream
				outputStream = videoOutputStream
				//更新时间戳，加上上一条视频最后一帧的时间戳
				if i != 0 {
					packet.SetPts(astiav.RescaleQ(lastVideoPacketTs.pts+1, inputStream.TimeBase(), outputStream.TimeBase()) + packet.Pts())
					packet.SetDts(astiav.RescaleQ(lastVideoPacketTs.dts+1, inputStream.TimeBase(), outputStream.TimeBase()) + packet.Dts())
				}
				//记录当前时间戳
				currVideoPacketTs.pts = packet.Pts()
				currVideoPacketTs.dts = packet.Dts()
			} else if packet.StreamIndex() == inputCtx.audioStream.Index() {
				inputStream = inputCtx.audioStream
				outputStream = audioOutputStream
				//更新时间戳，加上上一条视频最后一帧的时间戳
				if i != 0 {
					packet.SetPts(astiav.RescaleQ(lastAudioPacketTs.pts+1, inputStream.TimeBase(), outputStream.TimeBase()) + packet.Pts())
					packet.SetDts(astiav.RescaleQ(lastAudioPacketTs.dts+1, inputStream.TimeBase(), outputStream.TimeBase()) + packet.Dts())
				}
				//记录当前时间戳
				currAudioPacketTs.pts = packet.Pts()
				currAudioPacketTs.dts = packet.Dts()
			}
			// 更新数据帧参数
			packet.SetStreamIndex(outputStream.Index())
			// 时间戳尺度变换
			packet.RescaleTs(inputStream.TimeBase(), outputStream.TimeBase())
			packet.SetPos(-1)

			// 交叉写入视频输出缓冲区
			if err = outputFormatCtx.WriteInterleavedFrame(packet); err != nil {
				return errors.New(fmt.Sprintf("交叉写入视频帧失败: %s", err))
			}
			packet.Unref()
		}
		log.Printf("第%d个视频拼接完成", i+1)

	}

	//写入MP4文件尾
	if err = outputFormatCtx.WriteTrailer(); err != nil {
		return errors.New(fmt.Sprintf("写入MP4文件尾失败: %s", err))
	}

	bytes := videoBuf.Bytes()
	SaveFile(bytes, outputFileName)
	log.Printf("视频拼接成功，文件名：%s", outputFileName)
	return nil
}

func freeFormatContextList(ctxList []*InputContext) {
	for _, ctx := range ctxList {
		ctx.fmtCtx.Free()
	}
}

func SaveFile(videoBytes []byte, outputName string) {
	mp4File, err := os.Create(outputName)
	if err != nil {
		log.Fatalf("创建文件失败,文件名: %s", outputName)
	}
	defer mp4File.Close()
	_, err = mp4File.Write(videoBytes)
	if err != nil {
		log.Fatalf("写入数据失败,文件名: %s", outputName)
	}
}
