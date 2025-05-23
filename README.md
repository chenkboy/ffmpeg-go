# ffmpeg-go
**环境搭建**：

ffmep版本：n7.0+

添加环境变量

%FFMPEG_HOME%为FFmepg目录

**PKG_CONFIG_PATH=%FFMPEG_HOME%\build\lib\pkgconfig**

Path中添加**%FFMPEG_HOME%\build\lib**

**参数说明:**

1) **rtspUrl**:rtsp地址

2) **videoKey**:保存视频字节数组到redis列表的key

3) **audioKey**:保存音频字节数组到redis列表的key

4) **imageKey**:保存图片字节数组到redis列表的key

### 5**seconds**:视频时间

**1.抓取视频，不包含音频流**

要求视频编码格式为h264

视频格式为mp4

```go
CaptureVideoAndPushToRedis(rtspUrl string, videoKey string, seconds time.Duration)
```

**2.抓取视频和图片，不包含音频流**

要求视频编码格式为h264

视频格式为mp4，图片格式为jpg

```go
CaptureVideoImageAndPushToRedis(rtspUrl string, videoKey string, imageKey string, seconds time.Duration)
```

**3.抓取视频，音频和图片，包含音频流**

要求视频编码格式为h264,音频编码格式为pcm_ulaw

视频格式为mp4,音频格式为wav，图片格式为jpg

```go
CaptureVideoAudioImageAndPushToRedis(rtspUrl string, videoKey string, audioKey string, imageKey string, seconds time.Duration)
```

**o4.拼接视频**

将多段视频拼接成一个视频并保存到本地

videoKey:redis中视频列表的key

folderPath:本地文件目录

outputFileName:本地文件名

```go
ConcatVideos(videoKey, folderPath, outputFileName string)
```




### linux ffmpeg 动态库配置

下载ffmpeg源码包，ffmpeg-n7.0版本，进行解压，进入ffmpeg-n7.0，进行构建

```cmd
./configure  --enable-shared --disable-static --enable-gpl --extra-cflags=-I/usr/local/include --extra-ldflags=-L/usr/local/lib  --enable-pthreads
```

可能会有错误问题，一般是系统配置问题，按照错误提示修改即可，编译

```cmd
make
make install
```

添加动态链接库

在`/etc/ld.so.conf.d/`下面创建一个新文件，把ffmpeg动态链接库路径加进去

```cmd
vim /etc/ld.so.conf.d/ffmpeg-4.3.1.conf
#添加以下路径
/usr/local/lib

cat /etc/ld.so.conf.d/ffmpeg-4.3.1.conf
/usr/local/lib

#执行
ldconfig
```

添加环境变量

```cmd
vim ~/.bashrc
#添加一下cgo环境变量
export PKG_CONFIG_PATH=/usr/local/lib/pkgconfig:$PKG_CONFIG_PATH
export LD_LIBRARY_PATH=/usr/local/bin:$LD_LIBRARY_PATH
#生效
source ~/.profile
```

至此，Linux的ffmpeg的cgo环境配置完毕



tips:

cgo支持windows交叉编译，但是需要配置linux和Windows的c动态编译库，不然在windows下直接交叉编译，会提示

对应c的函数变量未定义。

### ffmpeg测试问题

docker刚装好cgo动态运行环境，若docker环境中原本有低版本的libc.so.6，则会有版本冲突，使用ls,top等多个基础命令，提示环境缺失，例如，在/app路径下，存在libc.so.6，会出现如下情况

```cmd
root@eb60b8fb7ce0:/app# ls
ls: libc.so.6: version `GLIBC_2.38' not found (required by ls)
ls: libc.so.6: version `GLIBC_2.38' not found (required by /lib/x86_64-linux-gnu/libselinux.so.1)
```

但是切换路径后，正常运行。

```cmd
root@eb60b8fb7ce0:/app# cd ffmpeg
root@eb60b8fb7ce0:/app/ffmpeg# ls
FFmpeg-n7.0.2 FFmpeg-n7.0.2.tar.gz test
```

解决方法：

一个简单但不完全适用的方法是，将app目录下的libc.so.6删除即可



warning:

version `GLIBC_2.38' not found 问题比较麻烦，若处置不当可能会导致docker环境崩溃，基本命令全部不可用，已踩过坑。
