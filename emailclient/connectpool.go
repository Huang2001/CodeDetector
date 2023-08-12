package emailclient

import (
	"fmt"
	"sync"
	"time"

	"github.com/douyu/jupiter/pkg/xlog"
	atomic2 "go.uber.org/atomic"
)

// 对外提供使用的结构体,管理所有的连接池
type EmailSenderPoolManager struct {
	poolConfig PoolCofig

	mutex         sync.Mutex
	connPools     map[string]emailSenderPool //邮箱平台与连接池之间的映射
	senderFactory map[string]SenderFactory   //不同邮箱协议与对应的工厂方法之间的映射，方便扩展
}

func (this *EmailSenderPoolManager) GetPool(connConfig ConnectConfig) (emailSenderPool, error) {
	//以邮件平台为细粒度作为key
	key := fmt.Sprintf("%s:%s|%s|%s|%s", connConfig.Host, connConfig.PortStr, connConfig.Username, connConfig.Password, connConfig.EmailProtocol)
	this.mutex.Lock()
	defer func() {
		this.mutex.Unlock()
	}()

	pool, ok := this.connPools[key]
	if ok {
		return pool, nil
	}

	factory := this.senderFactory[connConfig.EmailProtocol]
	newPool := newEmailSenderPool(this.poolConfig, factory, connConfig)
	err := newPool.initPool()
	if err != nil {
		xlog.Error("initConnPools err:", xlog.Any("err", err))
		return newPool, err
	}
	newPool.cleanIdleConn()

	this.connPools[key] = newPool
	return newPool, nil
}
func (this *EmailSenderPoolManager) ClosePool() {
	this.mutex.Lock()
	defer func() {
		this.mutex.Unlock()
	}()

	for key, pool := range this.connPools {
		pool.closePool()
		delete(this.connPools, key)
	}
}

// 连接池的接口
type emailSenderPool interface {
	GetSender() (EmailSender, error)
	PutSender(sender EmailSender)
	CloseConn(sender EmailSender) error

	initPool() error
	cleanIdleConn()
	closePool() error
}

// 包装Sender，具有判断超时功能
type timeoutEmailSender struct {
	sender      EmailSender
	lastUseTime time.Time
}

// 具体连接池的实现
type emailSenderPoolImp struct {
	poolConfig    PoolCofig
	connConfig    interface{}
	senderCache   chan timeoutEmailSender //带有缓冲区的通道缓存Sender
	factory       SenderFactory           //创建sender的工厂方法
	openedConnNum atomic2.Int32
	mutex         sync.Mutex
	running       bool
}

// 这里我改进了一下，采用 半同步半异步 初始化连接池;减少阻塞时间，也能给到异常情况
func (this *emailSenderPoolImp) initPool() error {
	this.running = true

	sender, err := this.factory(this.connConfig)
	if err != nil || sender == nil {
		xlog.Warn("create sender conn err:", xlog.Any("err", err))
		return err
	}
	this.senderCache <- timeoutEmailSender{
		sender:      sender,
		lastUseTime: time.Now(),
	}
	this.openedConnNum.Add(1)

	go func() {
		initialSize := this.poolConfig.InitialCap
		for true {
			if this.openedConnNum.Load() >= int32(initialSize) {
				break
			}
			sender, err := this.factory(this.connConfig)
			if err != nil && sender == nil {
				xlog.Warn("create sender conn err:", xlog.Any("err", err))
				continue
			}
			this.senderCache <- timeoutEmailSender{
				sender:      sender,
				lastUseTime: time.Now(),
			}
			this.openedConnNum.Add(1)

		}
	}()
	return nil
}

func (this *emailSenderPoolImp) GetSender() (EmailSender, error) {
	var sender EmailSender
	if !this.running {
		return sender, fmt.Errorf("conn pool is closed!")
	}

	timeout := this.poolConfig.IdleTimeout
	for true { //循环获取，直到拿到sender为止。
		select {
		case wrapSender := <-this.senderCache: //如果缓存中有则从缓存中拿
			if wrapSender.lastUseTime.Add(timeout).Before(time.Now()) { //超过最大空闲时间则关闭连接
				this.CloseConn(wrapSender.sender)
				continue
			}
			sender = wrapSender.sender
			return sender, nil
		default: //如果缓存中没有则考虑是否要创建连接
			if this.openedConnNum.Load() >= (int32)(this.poolConfig.MaxCap) { //试探性测试是否达到连接数量的最大值，避免频繁加锁
				timer := time.NewTimer(2 * time.Second)
				defer func(t *time.Timer) {
					t.Stop()
				}(timer)

				select {
				case wrapSender := <-this.senderCache:
					if wrapSender.lastUseTime.Add(timeout).Before(time.Now()) { //超过最大空闲时间则关闭连接
						this.CloseConn(wrapSender.sender)
						continue
					}
					sender = wrapSender.sender
					return sender, nil
				case <-timer.C:
					// 超过 2 秒后执行此分支，表示超时
					xlog.Debug("wait sender conn timeout 2 second")
				}
				continue
			}

			this.mutex.Lock()
			if this.openedConnNum.Load() >= (int32)(this.poolConfig.MaxCap) {
				this.mutex.Unlock()
				continue
			}
			this.openedConnNum.Add(1) //提前加数量，减少阻塞时间
			this.mutex.Unlock()

			var err error
			sender, err = this.factory(this.connConfig) //调用创建连接池时配置好的工厂方法和配置参数来创建连接
			if err != nil || sender == nil {
				this.openedConnNum.Add(-1)
				xlog.Warn("create sender conn err:", xlog.Any("err", err))
				return nil, err
			}

			return sender, nil
		}
	}
	return sender, nil

}

func (this *emailSenderPoolImp) PutSender(sender EmailSender) {
	if !this.running {
		this.CloseConn(sender)
	}

	this.senderCache <- timeoutEmailSender{
		sender:      sender,
		lastUseTime: time.Now(),
	}

}

func (this *emailSenderPoolImp) CloseConn(sender EmailSender) error {
	err := sender.Close()
	this.openedConnNum.Add(-1)
	return err
}

// 定时清理空闲连接
func (this *emailSenderPoolImp) cleanIdleConn() {
	go func() {
		timeout := this.poolConfig.IdleTimeout

		for this.running {
			time.Sleep(this.poolConfig.IdleTimeout * 2)

			for i := 0; i < int(this.openedConnNum.Load()); i++ {
				timeoutSender := <-this.senderCache
				if timeoutSender.lastUseTime.Add(timeout).Before(time.Now()) {
					this.CloseConn(timeoutSender.sender)
					continue
				}
				this.senderCache <- timeoutSender
			}
			// todo 这里是否要保证连接数量达到初始化容量大小

		}

	}()
}

func (this *emailSenderPoolImp) closePool() error {
	this.running = false

	var wrapedSender timeoutEmailSender
	isContinue := true
	for isContinue {
		select {
		case wrapedSender, isContinue = <-this.senderCache:
			this.CloseConn(wrapedSender.sender)
		default:
			isContinue = false
		}
	}

	close(this.senderCache)
	return nil

}
