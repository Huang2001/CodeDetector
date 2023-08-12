package emailclient

import "time"

const (
	//email协议名称：
	SMTP_PROTOCOL = "smtp"
)

// 池子本身的配置
type PoolCofig struct {
	//连接池中拥有的最小连接数
	InitialCap int
	//最大并发存活连接数
	MaxCap int
	//最大空闲连接
	MaxIdle int
	//检查连接是否有效的方法
	Ping func(interface{}) error
	//连接最大空闲时间，超过该时间则将失效
	IdleTimeout time.Duration
}

// 连接对应邮箱平台需要的数据   如何考虑预留字段？增加map或是嵌入一个结构体
type ConnectConfig struct {
	Host          string
	PortStr       string
	Username      string
	Password      string
	Encryption    string
	EmailProtocol string //具体的邮箱发送协议如SMTP等
	SendRetryNum  int
}

// 配置适配方法，将统一配置转换为对应协议需要的配置
func buildProtocolConnectConfig(config ConnectConfig) interface{} {
	if config.EmailProtocol == SMTP_PROTOCOL {
		return SmtpConnectConfig{
			Host:         config.Host,
			PortStr:      config.PortStr,
			Username:     config.Username,
			Password:     config.Password,
			Encryption:   config.Encryption,
			SendRetryNum: config.SendRetryNum,
		}
	}

	return nil

}

func newEmailSenderPool(poolConfig PoolCofig, factory SenderFactory, connConfig ConnectConfig) emailSenderPool {
	newPool := new(emailSenderPoolImp)
	newPool.poolConfig = poolConfig
	newPool.senderCache = make(chan timeoutEmailSender, poolConfig.MaxCap)
	newPool.factory = factory
	newPool.openedConnNum.Store(0)
	newPool.connConfig = buildProtocolConnectConfig(connConfig)
	return newPool
}

func NewEmailPoolManager(poolConfig PoolCofig) *EmailSenderPoolManager {
	poolsManager := new(EmailSenderPoolManager)
	poolsManager.connPools = make(map[string]emailSenderPool)

	senderFactoryMap := make(map[string]SenderFactory)
	senderFactoryMap[SMTP_PROTOCOL] = SmtpSenderFactory
	poolsManager.senderFactory = senderFactoryMap
	poolsManager.poolConfig = poolConfig

	return poolsManager
}
