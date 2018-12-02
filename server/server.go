package server

import (
	"context"
	"errors"
	"github.com/DrmagicE/gmqtt/logger"
	"github.com/DrmagicE/gmqtt/pkg/packets"
	"github.com/gorilla/websocket"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

var (
	ErrInvalWsMsgType = errors.New("invalid websocket message type") // [MQTT-6.0.0-1]
)

//Default configration
const (
	DefaultDeliveryRetryInterval = 20 * time.Second
	DefaultQueueQos0Messages     = true
	DefaultMaxInflightMessages   = 20
	DefaultMaxQueueMessages      = 2048
	DefaultMsgRouterLen          = 4096
	DefaultRegisterLen           = 2048
	DefaultUnRegisterLen         = 2048
)

type Server struct {
	mu              sync.RWMutex //gard clients map
	clients         map[string]*Client
	tcpListener     []net.Listener //tcp listeners
	websocketServer []*WsServer    //websocket server
	exitChan        chan struct{}
	retainedMsgMu   sync.Mutex
	retainedMsg     map[string]*packets.Publish //retained msg, key by topic name

	subscriptionsDB *subscriptionsDB  //store subscriptions

	msgRouter  chan *msgRouter  //
	register   chan *register   //register session
	unregister chan *unregister //unregister session

	config *config
	//hooks
	OnAccept    OnAccept
	OnConnect   OnConnect
	OnSubscribe OnSubscribe
	OnPublish   OnPublish
	OnClose     OnClose
	OnStop      OnStop
	//Monitor
	Monitor *Monitor
}

type subscriptionsDB struct {
	sync.RWMutex
	topicsById   map[string]map[string]packets.Topic //[clientId][topicName]Topic fast addressing with client id
	topicsByName map[string]map[string]packets.Topic //[topicName][clientId]Topic fast addressing with topic name
}

//init 初始化
func (db *subscriptionsDB) init(clientId string, topicName string) {
	if _, ok := db.topicsById[clientId]; !ok {
		db.topicsById[clientId] = make(map[string]packets.Topic)
	}
	if _, ok := db.topicsByName[topicName]; !ok {
		db.topicsByName[topicName] = make(map[string]packets.Topic)
	}
}
//return true if subscription is existed 判断订阅是否存在
func (db *subscriptionsDB) exist(clientId string, topicName string) bool {
	if _, ok := db.topicsByName[topicName][clientId]; !ok {
		return false
	}
	return true
}


//添加一条记录
func (db *subscriptionsDB) add(clientId string, topicName string, topic packets.Topic) {
	db.topicsById[clientId][topicName] = topic
	db.topicsByName[topicName][clientId] = topic
}
//删除一条记录
func (db *subscriptionsDB) remove(clientId string, topicName string) {
	if _ , ok := db.topicsByName[topicName]; ok {
		delete(db.topicsByName[topicName], clientId)
		if len(db.topicsByName[topicName]) == 0 {
			delete(db.topicsByName, topicName)
		}
	}
	if _ , ok := db.topicsById[clientId]; ok {
		delete(db.topicsById[clientId], topicName)
		if len(db.topicsById[clientId]) == 0 {
			delete(db.topicsById, clientId)
		}
	}
}


var log = &logger.Logger{}

func SetLogger(l *logger.Logger) {
	log = l
}

type config struct {
	deliveryRetryInterval time.Duration
	queueQos0Messages     bool
	maxInflightMessages   int
	maxQueueMessages      int
}

//session register
type register struct {
	client  *Client
	connect *packets.Connect
	error   error
}

//session unregister
type unregister struct {
	client *Client
	done   chan struct{}
}

type msgRouter struct {
	forceBroadcast bool
	clientIds      map[string]struct{} //key by clientId
	pub            *packets.Publish
}

func (srv *Server) registerHandler(register *register) {
	client := register.client
	defer close(client.ready)
	connect := register.connect
	var sessionReuse bool
	if connect.AckCode != packets.CODE_ACCEPTED {
		err := errors.New("reject connection, ack code:" + strconv.Itoa(int(connect.AckCode)))
		ack := connect.NewConnackPacket(false)
		client.out <- ack
		client.setError(err)
		register.error = err
		return
	}
	if srv.OnConnect != nil {
		code := srv.OnConnect(client)
		connect.AckCode = code
		if code != packets.CODE_ACCEPTED {
			err := errors.New("reject connection, ack code:" + strconv.Itoa(int(code)))
			ack := connect.NewConnackPacket(false)
			client.out <- ack
			client.setError(err)
			register.error = err
			return
		}
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	var oldSession *session
	oldClient, oldExist := srv.clients[client.opts.ClientId]
	srv.clients[client.opts.ClientId] = client
	if oldExist {
		oldSession = oldClient.session
		if oldClient.Status() == CONNECTED {
			if log != nil {
				log.Printf("%-15s %v: logging with duplicate ClientId: %s", "", client.rwc.RemoteAddr(), client.ClientOptions().ClientId)
			}
			oldClient.setSwitching()
			<-oldClient.Close()
			if oldClient.opts.WillFlag {
				willMsg := &packets.Publish{
					Dup:       false,
					Qos:       oldClient.opts.WillQos,
					Retain:    oldClient.opts.WillRetain,
					TopicName: []byte(oldClient.opts.WillTopic),
					Payload:   oldClient.opts.WillPayload,
				}
				go func() {
					msgRouter := &msgRouter{forceBroadcast: false, pub: willMsg}
					srv.msgRouter <- msgRouter
				}()
			}

			if !client.opts.CleanSession && !oldClient.opts.CleanSession { //reuse old session
				sessionReuse = true
			clearOut:
				for {
					select {
					case p := <-oldClient.out:
						if p, ok := p.(*packets.Publish); ok {
							oldClient.msgEnQueue(p)
						}
					default:
						break clearOut
					}
				}
			}
		} else if oldClient.Status() == DISCONNECTED {
			if !client.opts.CleanSession {
				sessionReuse = true
			}
		}
	}
	ack := connect.NewConnackPacket(sessionReuse)
	client.out <- ack
	client.setConnected()
	if sessionReuse { //发送还未确认的消息和离线消息队列 inflight & msgQueue
		client.session.maxInflightMessages = oldSession.maxInflightMessages
		client.session.maxQueueMessages = oldSession.maxQueueMessages
		client.session.unackpublish = oldSession.unackpublish
		oldSession.inflightMu.Lock()
		for e := oldSession.inflight.Front(); e != nil; e = e.Next() { //write unacknowledged publish & pubrel
			if inflight, ok := e.Value.(*InflightElem); ok {
				pub := inflight.Packet
				pub.Dup = true
				if inflight.Step == 0 {
					client.publish(pub)
				}
				if inflight.Step == 1 { //pubrel
					pubrel := pub.NewPubrec().NewPubrel()
					client.session.inflight.PushBack(inflight)
					client.session.setPacketId(pub.PacketId)
					client.out <- pubrel
				}
			}
		}
		oldSession.inflightMu.Unlock()
		oldSession.msgQueueMu.Lock()
		for e := oldSession.msgQueue.Front(); e != nil; e = e.Next() { //write offline msg
			if publish, ok := e.Value.(*packets.Publish); ok {
				client.publish(publish)
			}
		}
		oldSession.msgQueueMu.Unlock()
		if log != nil {
			log.Printf("%-15s %v: logined with session reuse", "", client.rwc.RemoteAddr())
		}
	} else {
		if oldExist {
			srv.subscriptionsDB.Lock()
			srv.removeClientSubscriptions(client.opts.ClientId)
			srv.subscriptionsDB.Unlock()
		}
		if log != nil {
			log.Printf("%-15s %v: logined with new session", "", client.rwc.RemoteAddr())
		}
	}
	if srv.Monitor != nil {
		srv.Monitor.Register(client, sessionReuse)
	}
}

func (srv *Server) unregisterHandler(unregister *unregister) {
	defer close(unregister.done)
	client := unregister.client
	client.setDisConnected()
	if client.session == nil {
		return
	}
clearIn:
	for {
		select {
		case p := <-client.in:
			if _, ok := p.(*packets.Disconnect); ok {
				client.cleanWillFlag = true
			}
		default:
			break clearIn
		}
	}

	if !client.cleanWillFlag && client.opts.WillFlag {
		willMsg := &packets.Publish{
			Dup:       false,
			Qos:       client.opts.WillQos,
			Retain:    false,
			TopicName: []byte(client.opts.WillTopic),
			Payload:   client.opts.WillPayload,
		}
		go func() {
			msgRouter := &msgRouter{forceBroadcast: false, pub: willMsg}
			client.server.msgRouter <- msgRouter
		}()
	}
	if client.opts.CleanSession {
		if log != nil {
			log.Printf("%-15s %v: logout & cleaning session", "", client.rwc.RemoteAddr())
		}
		srv.mu.Lock()
		delete(srv.clients, client.opts.ClientId)
		srv.subscriptionsDB.Lock()
		srv.removeClientSubscriptions(client.opts.ClientId)
		srv.subscriptionsDB.Unlock()
		srv.mu.Unlock()
	} else { //store session 保持session
		if log != nil {
			log.Printf("%-15s %v: logout & storing session", "", client.rwc.RemoteAddr())
		}
		//clear  out
	clearOut:
		for {
			select {
			case p := <-client.out:
				if p, ok := p.(*packets.Publish); ok {
					client.publish(p)
				}
			default:
				break clearOut
			}
		}
	}
	if srv.Monitor != nil {
		srv.Monitor.UnRegister(client.opts.ClientId, client.opts.CleanSession)
	}
}

func (srv *Server) msgRouterHandler(msg *msgRouter) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()
	pub := msg.pub
	if msg.forceBroadcast { //broadcast
		publish := pub.CopyPublish()
		publish.Dup = false
		if len(msg.clientIds) != 0 {
			for cid, _ := range msg.clientIds {
				if _, ok := srv.clients[cid]; ok {
					srv.clients[cid].publish(publish)
				}
			}
		} else {
			for _, c := range srv.clients {
				c.publish(publish)
			}
		}
		return
	}
	srv.subscriptionsDB.RLock()
	defer srv.subscriptionsDB.RUnlock()
	m := make(map[string]uint8)
	cidlen := len(msg.clientIds)
	for topicName, cmap := range srv.subscriptionsDB.topicsByName {
		if packets.TopicMatch(pub.TopicName, []byte(topicName)) { //找到能匹配当前主题订阅等级最高的客户端
			if cidlen != 0 { //to specific clients
				for cid := range msg.clientIds {
					if t, ok := cmap[cid]; ok {

							if qos, ok := m[cid]; ok {
								if t.Qos > qos {
									m[cid] = t.Qos
								}
							} else {
								m[cid] = t.Qos
							}

					}
				}
			} else {
				for cid, t := range cmap { //cmap:map[string]*subscription

						if qos, ok := m[cid]; ok {
							if t.Qos > qos {
								m[cid] = t.Qos
							}
						} else {
							m[cid] = t.Qos
						}

				}
			}
		}
	}
	for cid, qos := range m {
		publish := pub.CopyPublish()
		if publish.Qos > qos {
			publish.Qos = qos
		}
		publish.Dup = false
		if c, ok := srv.clients[cid]; ok {
			c.publish(publish)
		}
	}
}

//return whether it is a new subscription
func (srv *Server) subscribe(clientId string, topic packets.Topic) bool {
	var isNew bool
	srv.subscriptionsDB.init(clientId, topic.Name)
	isNew = !srv.subscriptionsDB.exist(clientId, topic.Name)
	srv.subscriptionsDB.topicsById[clientId][topic.Name] = topic
	srv.subscriptionsDB.topicsByName[topic.Name][clientId] = topic
	return isNew
}
func (srv *Server) unsubscribe(clientId string, topicName string) {
	srv.subscriptionsDB.remove(clientId,topicName)
}

func (srv *Server) removeClientSubscriptions(clientId string) {
	db := srv.subscriptionsDB
	if _ , ok := db.topicsById[clientId]; ok {
		for topicName, _ := range db.topicsById[clientId] {
			if _, ok := db.topicsByName[topicName];ok {
				delete(db.topicsByName[topicName],clientId)
				if len(db.topicsByName[topicName]) == 0 {
					delete(db.topicsByName, topicName)
				}
			}
		}
		delete(db.topicsById,clientId)
	}
}

//server event loop
func (srv *Server) eventLoop() {
	for {
		select {
		case register := <-srv.register:
			srv.registerHandler(register)
		case unregister := <-srv.unregister:
			srv.unregisterHandler(unregister)
		case msg := <-srv.msgRouter:
			srv.msgRouterHandler(msg)
		}
	}
}


type WsServer struct {
	Server   *http.Server
	CertFile string
	KeyFile  string
}

type OnAccept func(conn net.Conn) bool

type OnStop func()

//返回qos等级，或者是不允许订阅
//Allowed return codes:
//0x00 - Success - Maximum QoS 0
//0x01 - Success - Maximum QoS 1
//0x02 - Success - Maximum QoS 2
//0x80 - Failure
type OnSubscribe func(client *Client, topic packets.Topic) uint8

//返回qos等级，或者是不允许订阅
//Whether the publish packet will be delivered or not.
type OnPublish func(client *Client, publish *packets.Publish) bool

//tcp连接关闭之后触发
//called after tcp connection closed
type OnClose func(client *Client, err error)

//返回connack中响应码
//return the code of connack packet
type OnConnect func(client *Client) (code uint8)

func NewServer() *Server {
	return &Server{
		exitChan:    make(chan struct{}),
		clients:     make(map[string]*Client),
		msgRouter:   make(chan *msgRouter, DefaultMsgRouterLen),
		register:    make(chan *register, DefaultRegisterLen),
		unregister:  make(chan *unregister, DefaultUnRegisterLen),
		retainedMsg: make(map[string]*packets.Publish),
		subscriptionsDB: &subscriptionsDB{
			topicsByName: make(map[string]map[string]packets.Topic),
			topicsById:   make(map[string]map[string]packets.Topic),
		},
		config: &config{
			deliveryRetryInterval: DefaultDeliveryRetryInterval,
			queueQos0Messages:     DefaultQueueQos0Messages,
			maxInflightMessages:   DefaultMaxInflightMessages,
			maxQueueMessages:      DefaultMaxQueueMessages,
		},
		Monitor: &Monitor{
			Repository: &MonitorStore{
				clients:       make(map[string]ClientInfo),
				sessions:      make(map[string]SessionInfo),
				subscriptions: make(map[string]map[string]SubscriptionsInfo),
			},
		},
	}
}

func (srv *Server) SetMsgRouterLen(i int) {
	srv.msgRouter = make(chan *msgRouter, i)
}
func (srv *Server) SetRegisterLen(i int) {
	srv.register = make(chan *register, i)
}
func (srv *Server) SetUnregisterLen(i int) {
	srv.unregister = make(chan *unregister, i)
}

func (srv *Server) Publish(publish *packets.Publish, clientIds ...string) {
	cid := make(map[string]struct{})
	for _, id := range clientIds {
		cid[id] = struct{}{}
	}
	srv.msgRouter <- &msgRouter{false, cid, publish}
}

func (srv *Server) Broadcast(publish *packets.Publish, clientIds ...string) {
	cid := make(map[string]struct{})
	for _, id := range clientIds {
		cid[id] = struct{}{}
	}
	srv.msgRouter <- &msgRouter{true, cid, publish}
}

func (srv *Server) Subscribe(clientId string, topics []packets.Topic) {
	client := srv.Client(clientId)
	if client == nil {
		return
	}
	srv.subscriptionsDB.Lock()
	defer srv.subscriptionsDB.Unlock()
	for _, v := range topics {
		srv.subscribe(clientId, v)
		if srv.Monitor != nil {
			srv.Monitor.Subscribe(SubscriptionsInfo{
				ClientId: clientId,
				Qos:      v.Qos,
				Name:     string(v.Name),
				At:       time.Now(),
			})
		}
	}
}

func (srv *Server) UnSubscribe(clientId string, topics []string) {
	client := srv.Client(clientId)
	if client == nil {
		return
	}
	srv.subscriptionsDB.Lock()
	defer srv.subscriptionsDB.Unlock()
	for _, v := range topics {
		srv.unsubscribe(clientId, v)
		if srv.Monitor != nil {
			srv.Monitor.UnSubscribe(clientId, v)
		}
	}
}

func (srv *Server) SetDeliveryRetryInterval(duration time.Duration) {
	srv.config.deliveryRetryInterval = duration
}

func (srv *Server) SetMaxQueueMessages(nums int) {
	srv.config.maxQueueMessages = nums
}

func (srv *Server) SetQueueQos0Messages(b bool) {
	srv.config.queueQos0Messages = b
}

func (srv *Server) SetMaxInflightMessages(i int) {
	srv.config.maxInflightMessages = i
}

func (srv *Server) AddTCPListenner(ln ...net.Listener) {
	for _, v := range ln {
		srv.tcpListener = append(srv.tcpListener, v)
	}
}

func (srv *Server) AddWebSocketServer(Server ...*WsServer) {
	for _, v := range Server {
		srv.websocketServer = append(srv.websocketServer, v)
	}
}

func (srv *Server) serveTcp(l net.Listener) {
	defer func() {
		l.Close()
	}()
	var tempDelay time.Duration
	for {
		rw, e := l.Accept()
		if e != nil {
			if ne, ok := e.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				time.Sleep(tempDelay)
				continue
			}
			return
		}
		if srv.OnAccept != nil {
			if !srv.OnAccept(rw) {
				rw.Close()
				continue
			}
		}
		client := srv.newClient(rw)
		go client.serve()
	}
}

func (srv *Server) Client(clientId string) *Client {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.clients[clientId]
}


var defaultUpgrader = &websocket.Upgrader{
	ReadBufferSize:  READ_BUFFER_SIZE,
	WriteBufferSize: WRITE_BUFFER_SIZE,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
	Subprotocols: []string{"mqtt"},
}

//实现io.ReadWriter接口
//implement the io.ReadWriter
type wsConn struct {
	net.Conn
	c *websocket.Conn
}

func (ws *wsConn) Close() error {
	return ws.Conn.Close()
}

func (ws *wsConn) Read(p []byte) (n int, err error) {
	msgType, r, err := ws.c.NextReader()
	if err != nil {
		return 0, err
	}
	if msgType != websocket.BinaryMessage {
		return 0, ErrInvalWsMsgType
	}
	return r.Read(p)
}

func (ws *wsConn) Write(p []byte) (n int, err error) {
	err = ws.c.WriteMessage(websocket.BinaryMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), err
}

func (srv *Server) serveWebSocket(ws *WsServer) {
	var err error
	if ws.CertFile != "" && ws.KeyFile != "" {
		err = ws.Server.ListenAndServeTLS(ws.CertFile, ws.KeyFile)
	} else {
		err = ws.Server.ListenAndServe()
	}
	if err != http.ErrServerClosed {
		panic(err.Error())
	}
}

func (srv *Server) newClient(c net.Conn) *Client {
	client := &Client{
		server:        srv,
		rwc:           c,
		bufr:          newBufioReaderSize(c, READ_BUFFER_SIZE),
		bufw:          newBufioWriterSize(c, WRITE_BUFFER_SIZE),
		close:         make(chan struct{}),
		closeComplete: make(chan struct{}),
		error:         make(chan error, 1),
		in:            make(chan packets.Packet, READ_BUFFER_SIZE),
		out:           make(chan packets.Packet, WRITE_BUFFER_SIZE),
		status:        CONNECTING,
		opts:          &ClientOptions{},
		cleanWillFlag: false,
		ready:         make(chan struct{}),
	}
	client.packetReader = packets.NewReader(client.bufr)
	client.packetWriter = packets.NewWriter(client.bufw)
	client.setConnecting()
	client.newSession()
	return client
}

func (srv *Server) Run() {
	if srv.Monitor != nil {
		srv.Monitor.Repository.Open()
	}
	go srv.eventLoop()
	for _, ln := range srv.tcpListener {
		go srv.serveTcp(ln)
	}
	if len(srv.websocketServer) != 0 {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			c, err := defaultUpgrader.Upgrade(w, r, nil)
			if err != nil {
				log.Println("upgrade:", err)
				return
			}
			defer c.Close()
			conn := &wsConn{c.UnderlyingConn(), c}
			client := srv.newClient(conn)
			client.serve()
		})
	}
	for _, server := range srv.websocketServer {
		go srv.serveWebSocket(server)
	}
}

func (srv *Server) Stop(ctx context.Context) error {
	select {
	case <-srv.exitChan:
		return nil
	default:
		close(srv.exitChan)
	}
	for _, l := range srv.tcpListener {
		l.Close()
	}
	for _, ws := range srv.websocketServer {
		ws.Server.Shutdown(ctx)
	}
	//关闭所有的client
	//closing all client
	srv.mu.Lock()
	closeCompleteSet := make([]<-chan struct{}, len(srv.clients))
	i := 0
	for _, c := range srv.clients {
		closeCompleteSet[i] = c.Close()
		i++
	}
	srv.mu.Unlock()
	done := make(chan struct{})
	go func() {
		for _, v := range closeCompleteSet {
			//等所有的session退出完毕
			//wait for all sessions to unregister
			<-v
		}
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		if srv.Monitor != nil {
			srv.Monitor.Repository.Close()
		}
		if srv.OnStop != nil {
			srv.OnStop()
		}
		return nil
	}

}
