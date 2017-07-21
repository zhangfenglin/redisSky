package backend

import (
	"fmt"
	"strconv"
	"strings"

	"log"

	"github.com/garyburd/redigo/redis"
	"golang.org/x/net/websocket"
)

func scan(ws *websocket.Conn, c redis.Conn, key, field string, t scanType, iterate int64) (int64, []string) {

	var method, cmd string
	var ret []interface{}
	var err error
	switch t {
	case keyScan:
		method = "scan"
	case setScan:
		method = "sscan"
	case hashScan:
		method = "hscan"
	case zsetScan:
		method = "zscan"
	default:
		log.Println("type not exists!")
		return 0, nil
	}

	if t == keyScan {
		if !strings.ContainsAny(key, "*") {
			key = key + "*"
		}
		cmd = fmt.Sprintf("%s %d MATCH %s COUNT %d", method, iterate, key, _globalConfigs.System.KeyScanLimits)
		ret, err = redis.Values(c.Do(method, iterate, "MATCH", key, "COUNT", _globalConfigs.System.KeyScanLimits))
	} else {
		if key == "" {
			sendCmdError(ws, "key can't not be empty")
			return 0, nil
		}
		if !strings.ContainsAny(key, "*") {
			field = field + "*"
		}
		cmd = fmt.Sprintf("%s %s %d MATCH %s COUNT %d", method, key, iterate, field, _globalConfigs.System.KeyScanLimits)
		ret, err = redis.Values(c.Do(method, key, iterate, "MATCH", field, "COUNT", _globalConfigs.System.KeyScanLimits))
	}

	sendCmd(ws, cmd)
	if err != nil {
		sendCmdError(ws, "redis error: "+err.Error())
		return 0, nil
	}
	sendCmdReceive(ws, ret)
	keys, err := redis.Strings(ret[1], nil)
	if err != nil {
		sendCmdError(ws, "redis error: "+err.Error())
		return 0, nil
	}
	iterate, err = redis.Int64(ret[0], nil)
	if err != nil {
		sendCmdError(ws, "redis error: "+err.Error())
		return 0, nil
	}
	return iterate, keys
}

// ScanKeys scan redis key
func ScanKeys(ws *websocket.Conn, data interface{}) {
	if info, ok := checkOperData(ws, data); ok {
		key, ok := (info.Data).(string)
		if !ok {
			sendCmdReceive(ws, info.Data)
			sendCmdError(ws, "key should be string!")
			return
		}
		c, err := getRedisClient(info.ServerID, info.DB)
		if err != nil {
			sendCmdError(ws, "redis error: "+err.Error())
			return
		}
		defer c.Close()
		_, keys := scan(ws, c, key, "", keyScan, 0)
		var message Message
		message.Operation = "ScanKeys"
		message.Data = keys
		websocket.JSON.Send(ws, message)
	}
}

// GetKey get value of the key
func GetKey(ws *websocket.Conn, data interface{}) {
	if c, _redisValue, ok := checkRedisValue(ws, data); ok {
		defer c.Close()
		// type, ttl, data
		var key = _redisValue.Key
		extra, ok := (_redisValue.Val).(map[string]string)
		var field = ""
		if ok {
			field = extra["field"]
		}
		if t, err := keyType(ws, c, key); err == nil {
			if t == "none" {
				sendCmdError(ws, "key is not exists")
				return
			}
			_redisValue.T = t
			// ttl
			cmd := "TTL " + key
			sendCmd(ws, cmd)
			expire, err := redis.Int64(c.Do("TTL", key))
			if err != nil {
				sendCmdError(ws, err.Error())
				return
			}
			sendCmdReceive(ws, expire)
			_redisValue.TTL = expire

			switch t {
			case "string":
				cmd = "GET " + key
				sendCmd(ws, cmd)
				s, err := redis.String(c.Do("GET", key))
				if err != nil {
					sendCmdError(ws, err.Error())
					return
				}
				_redisValue.Val = s
			case "list":
				cmd = "LRANGE 0 " + strconv.Itoa(_globalConfigs.System.RowScanLimits)
				list, err := redis.Strings(c.Do("LRANGE", 0, _globalConfigs.System.RowScanLimits))
				if err != nil {
					sendCmdError(ws, err.Error())
					return
				}
				_redisValue.Val = list
			case "set":
				_, vals := scan(ws, c, key, field, setScan, 0)
				_redisValue.Val = vals
			case "zset", "hash":
				var method scanType
				if t == "zset" {
					method = zsetScan
				} else {
					method = hashScan
				}
				_, vals := scan(ws, c, key, field, method, 0)
				tmp := make(map[string]string)
				for i := 0; i < len(vals); i = i + 2 {
					tmp[vals[0]] = vals[1]
				}
				_redisValue.Val = tmp
			}

			message := Message{
				Operation: "GetKey",
				Data:      _redisValue,
			}
			websocket.JSON.Send(ws, message)
		}
	}
}

// SetTTL set ttl
func SetTTL(ws *websocket.Conn, data interface{}) {
	if c, _redisValue, ok := checkRedisValue(ws, data); ok {
		defer c.Close()
		cmd := "EXPIRE " + _redisValue.Key + " " + strconv.FormatInt(_redisValue.TTL, 10)
		sendCmd(ws, cmd)
		expire, err := redis.Int(c.Do("EXPIRE", _redisValue.Key, _redisValue.TTL))
		if err != nil {
			sendCmdError(ws, "redis error: "+err.Error())
			return
		}
		sendCmdReceive(ws, expire)

		message := Message{
			Operation: "SetTTL",
			Data:      expire,
		}
		websocket.JSON.Send(ws, message)

	}
}

// KeyType type of the key
func KeyType(ws *websocket.Conn, data interface{}) {
	if c, _redisValue, ok := checkRedisValue(ws, data); ok {
		defer c.Close()
		s, err := keyType(ws, c, _redisValue.Key)
		if err == nil {
			message := &Message{
				Operation: "KeyType",
				Data:      s,
			}
			websocket.JSON.Send(ws, message)
		}
	}
}

// Rename rename a key
func Rename(ws *websocket.Conn, data interface{}) {
	if c, _redisValue, ok := checkRedisValue(ws, data); ok {
		defer c.Close()
		newKey, ok := (_redisValue.Val).(string)
		if !ok {
			sendCmdError(ws, "data should be string of the new key")
			return
		}
		cmd := "RENAME " + _redisValue.Key + " " + newKey
		sendCmd(ws, cmd)
		_, err := c.Do("RENAME", _redisValue.Key, newKey)
		if err != nil {
			sendCmdError(ws, "redis error: "+err.Error())
			return
		}
		message := &Message{
			Operation: "Rename",
			Data:      newKey,
		}
		websocket.JSON.Send(ws, message)
	}
}

/*
 none (key不存在)
 string (字符串)
 list (列表)
 set (集合)
 zset (有序集)
 hash (哈希表)
*/
func keyType(ws *websocket.Conn, c redis.Conn, key string) (string, error) {
	cmd := "TYPE " + key
	sendCmd(ws, cmd)
	s, err := redis.String(c.Do("TYPE", key))
	if err != nil {
		sendCmdError(ws, err.Error())
		return "", err
	}
	sendCmdReceive(ws, s)
	return s, err
}