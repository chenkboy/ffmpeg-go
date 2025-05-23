package redis

import (
	"encoding/json"
	"errors"
	"github.com/gomodule/redigo/redis"
	"log"
	"math"
	"os"
	"strconv"
	"time"
)

// RedisClient RedisClient实列
type RedisClient struct {
	redisPool *redis.Pool
}

// RedisConf redis链接池配置信息
type RedisConf struct {
	Host        string `json:"host"`
	MaxIdle     int    `json:"maxIdle"`
	MaxActive   int    `json:"maxActive"`
	IdleTimeout int    `json:"idleTimeout"`
	Password    string `json:"password"`
	Db          int    `json:"db"`
}

// NewClient 新建redis客户端
func NewClient(redisConf *RedisConf) (*RedisClient, error) {
	var redisClient = new(RedisClient)
	err := redisClient.InitRedis(redisConf)
	return redisClient, err
}

// DefaultClient 默认redis客户端
func DefaultClient() (*RedisClient, error) {
	bytes := make([]byte, 0)
	bytes, err := os.ReadFile("redis_util/config.json")
	if err != nil {
		log.Fatal("配置文件读取失败", err)
		return nil, err
	}
	redisConf := &RedisConf{}
	err = json.Unmarshal(bytes, redisConf)
	if err != nil {
		log.Fatal("反序列化失败", err)
		return nil, err
	}
	redisClient, _ := NewClient(redisConf)
	return redisClient, nil

}

// InitRedis 初始化redis
func (redisClient *RedisClient) InitRedis(redisconf *RedisConf) error {
	redisClient.redisPool = &redis.Pool{
		MaxIdle:     redisconf.MaxIdle,
		MaxActive:   redisconf.MaxActive,
		IdleTimeout: time.Duration(redisconf.IdleTimeout) * time.Second,

		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", redisconf.Host)
			if err != nil {
				return nil, err
			}
			if _, err := c.Do("AUTH", redisconf.Password); err != nil {
				c.Close()
				return nil, err
			}
			if _, err := c.Do("SELECT", redisconf.Db); err != nil {
				c.Close()
				return nil, err
			}
			return c, nil
		},

		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},
	}

	if redisClient.redisPool == nil {
		return errors.New("链接池初始化失败")
	}

	return nil
}

// redis 设置
func (redisClient *RedisClient) Set(key string, value interface{}) error {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if _, err := conn.Do("SET", key, value); err != nil {
		return err
	}
	return nil

}

// SetEx redis 设置
func (redisClient *RedisClient) SetEx(key string, exp int, value interface{}) error {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if _, err := conn.Do("SETEX", key, exp, value); err != nil {
		return err
	}
	return nil

}

// AddGet redis 获取
func (redisClient *RedisClient) Get(key string) (interface{}, error) {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if result, err := conn.Do("GET", key); err != nil {
		return "", err
	} else {
		return result, nil
	}
}

// AddGet redis 获取
func (redisClient *RedisClient) GetString(key string) (string, error) {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	return redis.String(conn.Do("GET", key))
}

// AddGet redis 获取
func (redisClient *RedisClient) GetInt(key string) (int64, error) {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	return redis.Int64(conn.Do("GET", key))
}

func (redisClient *RedisClient) GetBytes(key string) ([]byte, error) {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	return redis.Bytes(conn.Do("GET", key))
}

// Del redis 获取
func (redisClient *RedisClient) Del(key string) error {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if _, err := conn.Do("DEL", key); err != nil {
		return err
	} else {
		return nil
	}
}

// Push redis
func (redisClient *RedisClient) Push(topic string, value interface{}) error {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if _, err := conn.Do("RPUSH", topic, value); err != nil {
		return err
	}
	return nil

}

// 返回列表的长度
func (redisClient *RedisClient) GetPopCount(topic string) (int64, error) {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	return redis.Int64(conn.Do("LLEN", topic))
}

//// Pop redis
//func (redisClient *RedisClient) Pop(topic string, timeout int) (interface{}, error) {
//	conn := redisClient.redisPool.Get()
//	defer conn.Close()
//	if result, err := conn.Do("BRPOP", topic, timeout); err != nil {
//		return "", err
//	} else {
//		if result != nil && len(result.([]interface{})) > 1 {
//			data := result.([]interface{})[1]
//			return data, nil
//		}
//		return "", errors.New("获取队列元素异常")
//	}
//}

// Pop redis
func (redisClient *RedisClient) Pop(topic string) (interface{}, error) {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if result, err := conn.Do("LPOP", topic); err != nil {
		return "", err
	} else {
		if result != nil {
			return result, nil
		}
		return "", errors.New("获取队列元素异常")
	}
}

func (redisClient *RedisClient) GetAllElements(topic string) ([]interface{}, error) {
	// 从连接池获取一个连接
	conn := redisClient.redisPool.Get()
	defer conn.Close()

	// 使用 LRANGE 命令获取列表中的所有元素
	result, err := redis.Values(conn.Do("LRANGE", topic, 0, -1))
	if err != nil {
		return nil, err
	}

	return result, nil
}

// ZCount 获取zset集合指定分数段内的元素个数
func (redisClient *RedisClient) ZCount(key string, min int64, max int64) (interface{}, error) {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if result, err := conn.Do("ZCOUNT", key, min, max); err != nil {
		return "", err
	} else {
		return result, nil
	}
}

// ZAdd 往zset集合中添加元素
func (redisClient *RedisClient) ZAdd(key string, score int64, value interface{}) error {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if _, err := conn.Do("ZADD", key, score, value); err != nil {
		return err
	} else {
		return nil
	}
}

// ZRemRangeByScore 移除zset集合中分数在 min和max之间的元素
func (redisClient *RedisClient) ZRemRangeByScore(key string, min int64, max int64) error {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if _, err := conn.Do("ZREMRANGEBYSCORE", key, min, max); err != nil {
		return err
	} else {
		return nil
	}
}

// Expire 设置过期时间
func (redisClient *RedisClient) Expire(key string, exp int64) error {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if _, err := conn.Do("EXPIRE", key, exp); err != nil {
		return err
	} else {
		return nil
	}
}

// 获取hash长度
func (redisClient *RedisClient) Hlen(key string) (int64, error) {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	return redis.Int64(conn.Do("HLEN", key))
}

// set hash
func (redisClient *RedisClient) Hset(topic string, field string, value interface{}) error {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if _, err := conn.Do("HSET", topic, field, value); err != nil {
		return err
	}
	return nil
}

// set hash 并设置过期时间 (不太行)
func (redisClient *RedisClient) HsetExpire(topic string, field string, value interface{}, t int) error {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if _, err := conn.Do("SET", topic+":"+field, value); err != nil {
		return err
	}
	if _, err := conn.Do("EXPIRE", topic+":"+field, t); err != nil {
		return err
	}
	return nil
}

// 删除 hash表字段
func (redisClient *RedisClient) Hdel(topic string, field string) error {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if _, err := conn.Do("HDEL", topic, field); err != nil {
		return err
	}
	return nil
}

// get hash表指定字段值
func (redisClient *RedisClient) Hget(topic string, field string) (interface{}, error) {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if result, err := conn.Do("HGET", topic, field); err != nil {
		return 0, err
	} else {
		if result != nil {
			v := 0
			l := len(result.([]uint8))
			for i, u := range result.([]uint8) {
				t, _ := strconv.Atoi(string(u))
				w := math.Pow(10, float64(l-i-1))
				v += t * int(w)
			}
			return v, nil
		}
	}
	return 0, nil
}

// 判断哈希表（Hash）中是否存在特定的 key; 返回 1（存在） 或 0（不存在）
func (redisClient *RedisClient) Hexists(topic string, field string) (interface{}, error) {
	conn := redisClient.redisPool.Get()
	defer conn.Close()
	if result, err := conn.Do("HEXISTS", topic, field); err != nil {
		return int64(0), err
	} else {
		return result, nil
	}
	return int64(0), nil
}
