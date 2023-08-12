package emailclient

import (
	"crypto/tls"
	"fmt"
	"strconv"
	"time"

	"github.com/douyu/jupiter/pkg/xlog"
	"github.com/go-gomail/gomail"
	commutils "ums-sender/internal/common/utils"
)

type SmtpSender struct {
	realSender gomail.SendCloser
}

func (this *SmtpSender) SendMsg(msg any) error {
	mail := msg.(*gomail.Message)
	return gomail.Send(this.realSender, mail)
}

func (this *SmtpSender) Close() error {
	return this.realSender.Close()
}

func (this *SmtpSender) GetConn() interface{} {
	return this.realSender
}

type SmtpConnectConfig struct { //与SMTP平台对接的配置
	Host         string
	PortStr      string
	Username     string
	Password     string
	Encryption   string
	SendRetryNum int
}

// SMTP协议Sender创建工厂方法
func SmtpSenderFactory(config any) (EmailSender, error) {
	c := config.(SmtpConnectConfig)

	sendRetryNum := c.SendRetryNum
	portStr := c.PortStr
	userName := c.Username
	password := c.Password
	host := c.Host

	if sendRetryNum <= 0 {
		sendRetryNum = 3
	}
	port, _ := strconv.Atoi(portStr)
	d := gomail.NewDialer(host, port, userName, commutils.JGPubDecode(password))
	d.TLSConfig = &tls.Config{InsecureSkipVerify: true}

	var err error
	var s gomail.SendCloser
	for i := 0; i < sendRetryNum; i++ {
		s, err = d.Dial()
		if err != nil {
			xlog.Warn(fmt.Sprintf("Dial fail. err:%s  host:%s  port:%s,  username:%s", err, host, portStr, userName))
			time.Sleep(100 * time.Millisecond)
			continue
		}
		break
	}

	sender := SmtpSender{
		realSender: s,
	}

	if err != nil || s == nil {
		return &sender, fmt.Errorf("Dial fail, %v", err)
	}
	return &sender, nil

}
