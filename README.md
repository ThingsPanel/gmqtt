[English](https://github.com/DrmagicE/gmqtt/blob/master/README.EN.md)
# Gmqtt [![Build Status](https://travis-ci.org/DrmagicE/gmqtt.svg?branch=master)](https://travis-ci.org/DrmagicE/gmqtt) [![codecov](https://codecov.io/gh/DrmagicE/gmqtt/branch/master/graph/badge.svg)](https://codecov.io/gh/DrmagicE/gmqtt)



# 更新日志
## 2018.12.2
* 优化订阅存储结构，大幅提高并发能力和减少响应时间
* 更新优化后的压力测试结果
## 2018.11.25
* 增加压力测试工具
* 优化部分代码结构
* 改变订阅主题的存储方式，优化转发性能
* 修改OnClose钩子方法，增加连接关闭原因
## 2018.11.18
* 暂时删除了session持久化功能，需要重新设计
* 新增运行状态监控/管理功能，在`cmd/broker`中通过restapi呈现
* 新增服务端触发的发布/订阅功能，在`cmd/broker`中通过restapi呈现
* 为session增加了缓存队列
* 重构部分代码，bug修复

# 本库的内容有：
* 基于Go语言实现的V3.1.1版本的MQTT服务器
* 提供MQTT服务器开发库，使用该库可以二次开发出功能更丰富的MQTT服务器应用
* MQTT V3.1.1 版本的协议解析库
* MQTT压力测试工具 [README.md](https://github.com/DrmagicE/gmqtt/blob/master/cmd/benchmark/README.md)

# 功能特性
* 内置了许多实用的钩子方法，使用者可以方便的定制需要的MQTT服务器（鉴权,ACL等功能）
* 支持tls/ssl以及ws/wss
* ~~提供session持久化功能~~ (暂时去掉，需要重新设计)
* 提供服务状态监控/管理api
* 提供发布/订阅/取消订阅api

# 安装
```$ go get github.com/DrmagicE/gmqtt/cmd/broker```
# 开始

## 使用内置的MQTT服务器
下列命令将监听`1883`端口[tcp]和`8080`端口[websocket]，开启MQTT服务器。
```
$ cd cmd/broker
$ go run main.go 
```
### 内置MQTT服务器配置文件
使用yaml的配置文件格式，例子见：`cmd/broker/config.yaml`
```
# 超时重传间隔秒数，默认20秒
delivery_retry_interval: 20
# 最大飞行窗口，默认20条
max_inflight_messages: 20
# 是否为离线的保持会话客户端转发QoS0消息，默认转发
queue_qos0_messages: true
# 缓存队列最大容量，默认20条
max_msgqueue_messages: 20
# pprof 监控文件，默认不开启pprof
# pprof.cpu CPU监控文件
# pprof.mem 内存监控文件
profile: {cpu: "cpuprofile", mem: "memprofile"}
# 是否打印日志，调试时使用，默认false不打印
logging: false
# http_server服务监听地址
# http_server.addr http服务监听地址
# http_server.user http basic auth的用户名密码,key是用户名，value是密码
http_server: {addr: ":9090",user: { admin: "admin"}}

# listener
# listener.$.protocol 支持mqtt或者websocket
# listener.$.addr 监听的端口,  用作填充net.Listen(network, address string) 中的address参数
# listener.$.certfile 如果使用tls/ssl，填写cert文件路径
# listenr.$.keyfile 如果使用tls/ssl，填写key文件路径
listener:
- {protocol: mqtt, addr: ':1883', certfile: , keyfile:  }
- {protocol: websocket, addr: ':8080', certfile: ,keyfile: }



```
默认使用`cmd/broker/config.yaml`配置文件，使用下列命令可以设置配置文件路径
```
$ go run main.go -config <config-file-path>
```

### 内置服务器的REST服务
通过HTTP Basic Auth进行鉴权，用户名密码配置见配置文件
#### 获取所有在线客户端
请求格式：
```
GET /clients?page=xxx&per-page=xxx
page:请求页数，不传默认第一页
per-page:每一页的条数，不传默认20条
```
响应格式：
```
{
    "list": [
        {
            "client_id": "1",
            "username": "publishonly",
            "remote_addr": "127.0.0.1:56359",
            "clean_session": true,
            "keep_alive": 60,
            "connected_at": "2018-11-18T02:10:36.6958382+08:00"
        }
    ],
    "page": 1,
    "page_size": 20,
    "current_count": 1,
    "total_count": 1,
    "total_page": 1
}

```

#### 获取指定客户端id的客户端
请求格式：
```
GET /client/:id
```
响应格式：
```
{
    "client_id": "1",
    "username": "publishonly",
    "remote_addr": "127.0.0.1:56359",
    "clean_session": true,
    "keep_alive": 60,
    "connected_at": "2018-11-18T02:10:36.6958382+08:00"
}
```


#### 获取所有会话（session）

请求格式：
```
GET /sessions?page=xxx&per-page=xxx
page:请求页数，不传默认第一页
per-page:每一页的条数，不传默认20条
```
响应格式：
```
{
    "list": [
        {
            "client_id": "1",
            "status": "online",
            "remote_addr": "127.0.0.1:56359",
            "clean_session": true,
            "subscriptions": 0,
            "max_inflight": 20,
            "inflight_len": 0,
            "max_msg_queue": 20,
            "msg_queue_len": 0,
            "msg_queue_dropped": 0,
            "connected_at": "2018-11-18T02:10:36.6958382+08:00",
            "offline_at": "0001-01-01T00:00:00Z"
        }
    ],
    "page": 1,
    "page_size": 20,
    "current_count": 1,
    "total_count": 1,
    "total_page": 1
}
```

#### 获取指定客户端id的会话

请求格式：
```
GET /session/:id
```
响应格式：
```
{
    "client_id": "1",
    "status": "online",
    "remote_addr": "127.0.0.1:56359",
    "clean_session": true,
    "subscriptions": 0,
    "max_inflight": 20,
    "inflight_len": 0,
    "max_msg_queue": 20,
    "msg_queue_len": 0,
    "msg_queue_dropped": 0,
    "connected_at": "2018-11-18T02:10:36.6958382+08:00",
    "offline_at": "0001-01-01T00:00:00Z"
}
```

#### 获取所有订阅主题信息

请求格式：
```
GET /subscriptions
```
响应格式：
```
{
    "list": [
        {
            "client_id": "1",
            "qos": 0,
            "name": "test8",
            "at": "2018-11-18T02:14:46.4582717+08:00"
        },
        {
            "client_id": "2",
            "qos": 2,
            "name": "123",
            "at": "2018-11-18T02:14:46.4582717+08:00"
        }
    ],
    "page": 1,
    "page_size": 20,
    "current_count": 2,
    "total_count": 2,
    "total_page": 1
}
```

#### 获取指定会话的订阅主题信息

请求格式：
```
GET /subscriptions/:id
```
响应格式：
```
{
    "list": [
        {
            "client_id": "1",
            "qos": 0,
            "name": "test8",
            "at": "2018-11-18T02:14:46.4582717+08:00"
        },
        {
            "client_id": "1",
            "qos": 2,
            "name": "123",
            "at": "2018-11-18T02:14:46.4582717+08:00"
        }
    ],
    "page": 1,
    "page_size": 20,
    "current_count": 2,
    "total_count": 2,
    "total_page": 1
}
```



#### 发布主题

请求格式：
```
POST /publish
```
POST请求参数：
```
qos : qos等级
topic : 发布的主题名称
payload : 主题payload
```

响应格式：
```
{
    "code": 0,
    "result": []
}
```

#### 订阅主题

请求格式：
```
POST /subscribe
```
POST请求参数：
```
qos : qos等级
topic : 订阅的主题名称
clientId : 订阅的客户端id
```

响应格式：
```
{
    "code": 0,
    "result": []
}
```

#### 取消订阅

请求格式：
```
POST /unsubscribe
```
POST请求参数：
```
topic : 需要取消订阅的主题名称
clientId : 需要取消订阅的客户端id
```

响应格式：
```
{
    "code": 0,
    "result": []
}
```

## 使用MQTT服务器开发库
当前内置的MQTT服务器功能比较弱，鉴权，ACL等功能均没有实现，建议采用MQTT服务器库进行二次开发：
```
func main() {
	s := server.NewServer()
	ln, err := net.Listen("tcp",":1883")
	if err != nil {
		log.Fatalln(err.Error())
		return
	}
	crt, err := tls.LoadX509KeyPair("../testcerts/server.crt", "../testcerts/server.key")
	if err != nil {
		log.Fatalln(err.Error())
		return
	}
	tlsConfig := &tls.Config{}
	tlsConfig.Certificates = []tls.Certificate{crt}
	tlsln, err := tls.Listen("tcp",":8883",tlsConfig)
	if err != nil {
		log.Fatalln(err.Error())
		return
	}
	s.AddTCPListenner(ln)
	s.AddTCPListenner(tlsln)
	//在Run()之前可以设置配置参数，以及钩子方法等
	s.OnConnect = .... 可实现一些鉴权功能
	s.OnSubscribe = .... 可实现ACL功能
	s.SetQueueQos0Messages(false)
	....
	
	
	s.Run()
	fmt.Println("started...")
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)
	<-signalCh
	s.Stop(context.Background())
	fmt.Println("stopped")
}

```
更多的使用例子可以参考`\examples`，里面介绍了全部钩子的使用方法。


# 文档说明
## 钩子方法
Gmqtt实现了下列钩子方法
* OnAccept  (仅支持在tcp/ssl下,websocket不支持)
* OnConnect 
* OnSubscribe
* OnPublish
* OnClose
* OnStop

在 `/examples/hook` 中有钩子的使用方法介绍。

### OnAccept
当使用tcp或者ssl方式连接的时候，该钩子方法会在`net.Listener.Accept`之后调用，
如果返回false，则会直接关闭tcp连接。
```
//If returns is `false`, it will close the `net.Conn` directly
type OnAccept func(conn net.Conn) bool
```
该钩子方法可以拒绝一些非法链接，可以用做自定义黑名单，连接速率限制等功能。

### OnConnect()
接收到登录报文之后会调用该方法。
该方法返回CONNACK报文当中的code值。
```
//return the code of connack packet
type OnConnect func(client *Client) (code uint8)
```
该方法可以用作鉴权实现，比如：
```
...
server.OnConnect = func(client *server.Client) (code uint8) {
  username := client.ClientOptions().Username
  password := client.ClientOptions().Password
  if validateUser(username, password) { //鉴权信息可以保存在数据库，文件，内存等地方
    return packets.CODE_ACCEPTED
  } else {
    return packets.CODE_BAD_USERNAME_OR_PSW
  }
}

```
### OnSubscribe()
接收到SUBSCRIBE报文之后调用。
该方法返回允许当前订阅主题的最大QoS等级。
```
//允许的一些返回值:
//0x00 - 成功 - 最大 QoS 0
//0x01 - 成功 - 最大 QoS 1
//0x02 - 成功 - 最大 QoS 2
//0x80 - 订阅失败
type OnSubscribe func(client *Client, topic packets.Topic) uint8
```
该方法可以用作实现ACL访问控制，比如：
```
...
server.OnSubscribe = func(client *server.Client, topic packets.Topic) uint8 {
  if client.ClientOptions().Username == "root" { //root用户想订阅什么就订阅什么
    return topic.Qos
  } else {
    if topic.Qos <= packets.QOS_1 {
      return topic.Qos
    }
    return packets.QOS_1   //对于其他用户，最多只能订阅到QoS1等级
  }
  
}
```

### OnPublish()
接收到PUBLISH报文之后调用。
```
//返回该报文是否会被继续分发下去
type OnPublish func(client *Client, publish *packets.Publish) bool
```
比如：
```
...
server.OnPublish = func(client *server.Client, publish *packets.Publish)  bool {
  if client.ClientOptions().Username == "subscribeonly" {
    client.Close()  //2.close the Network Connection
    return false
  }
  //Only qos1 & qos0 are acceptable(will be delivered)
	if publish.Qos == packets.QOS_2 {
    return false  //1.make a positive acknowledgement but not going to distribute the packet
  }
  return true
}
```
>If a Server implementation does not authorize a PUBLISH to be performed by a Client; it has no way of informing that Client. It MUST either 1.make a positive acknowledgement, according to the normal QoS rules, or 2.close the Network Connection [MQTT-3.3.5-2].

### OnClose()
当网络连接关闭之后调用
```
//This is called after Network Connection close
type OnClose func(client *Client, err error)
```

### OnStop()
但mqtt服务停止的时候调用
```
type OnStop func()
```


## 服务停止流程
调用 `server.Stop()` 将服务优雅关闭:
1. 关闭所有的`net.Listener`
2. 关闭所有的client，一直等待，直到所有的client的`OnClose`方法调用完毕
3. 退出

# 测试
## 单元测试
```
$ cd server
$ go test 
```
```
$ cd pkg/packets
$ go test
```
## 集成测试
通过了 [paho.mqtt.testing](https://github.com/eclipse/paho.mqtt.testing).

## 压力测试
[文档与测试结果](https://github.com/DrmagicE/gmqtt/blob/master/cmd/benchmark/README.md)

# TODO 
* 完善文档
* 性能对比[EMQ/Mosquito]
* Vendoring
* 网页监控
