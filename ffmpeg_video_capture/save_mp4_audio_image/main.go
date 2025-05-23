package main

import (
	redis "ffmpeg_video_capture/redis_util"
	"fmt"
	"log"
	"os"
)

var redisHost = "127.0.0.1:6379"
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

	videoDataList, err := redisClient.GetAllElements("VideoData")
	if err != nil {
		log.Fatal("获取视频数据出错")
	}
	if len(videoDataList) == 0 {
		log.Println("无视频数据")
		return
	} else {
		log.Printf("获取到%d条视频数据", len(videoDataList))
	}

	for i, videoData := range videoDataList {
		videoBytes := videoData.([]byte)
		SaveFile(videoBytes, fmt.Sprintf("output%d.mp4", i))
	}

	audioDataList, err := redisClient.GetAllElements("AudioData")
	if err != nil {
		log.Fatal("获取音频数据出错")
	}
	if len(audioDataList) == 0 {
		log.Println("无音频数据")
		return
	} else {
		log.Printf("获取到%d条音频数据", len(audioDataList))
	}

	for i, audioData := range audioDataList {
		audioBytes := audioData.([]byte)
		SaveFile(audioBytes, fmt.Sprintf("output%d.wav", i))
	}

	imageDataList, err := redisClient.GetAllElements("ImageData")
	if err != nil {
		log.Fatal("获取图片数据出错")
	}
	if len(imageDataList) == 0 {
		log.Println("无图片数据")
		return
	} else {
		log.Printf("获取到%d条图片数据", len(imageDataList))
	}

	for i, imageData := range imageDataList {
		imageBytes := imageData.([]byte)
		SaveFile(imageBytes, fmt.Sprintf("output%d.jpg", i))
	}

	log.Println("success")
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
