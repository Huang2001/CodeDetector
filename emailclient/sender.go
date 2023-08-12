package emailclient

// 顶层的emailSender,每种类型的邮件协议都需要实现这个接口来实现自定义的EmailSender(统一）
type EmailSender interface {
	SendMsg(interface{}) error
	Close() error
	GetConn() interface{} //这个方法直接返回真实的连接，方便做更灵活操作
}

//sender创建的工厂方法，每添加一种协议都需要实现这个工厂方法。达到创建连接的统一
type SenderFactory func(config any) (EmailSender, error)
