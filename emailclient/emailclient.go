package emailclient

import (
	"fmt"
	"mime"
	"time"

	"github.com/douyu/jupiter/pkg/xlog"
	"github.com/go-gomail/gomail"
	data "ums-sender/internal/common/struct/email"
	"ums-sender/internal/common/struct/sender"
)

type emailClient struct {
	emailInfo       *sender.EmailInfo
	connPool        emailSenderPool
	sendTryNum      int
	sendTryInterval time.Duration
}

// 这里默认只能发送SMTP协议，如果需要新增其它协议需要修改emailClient的发送方法
func NewEmailClient(emailInfo *sender.EmailInfo, poolManage *EmailSenderPoolManager, sendTryNum int, sendTryInterval time.Duration) (*emailClient, error) {
	connConfig := ConnectConfig{
		Host:          emailInfo.SendServerAddr,
		PortStr:       emailInfo.SendServerPort,
		Username:      emailInfo.Username,
		Password:      emailInfo.Password,
		Encryption:    emailInfo.SendServerEncryption,
		EmailProtocol: SMTP_PROTOCOL, //具体的邮箱发送协议如SMTP等
		SendRetryNum:  sendTryNum,
	}
	pool, err := poolManage.GetPool(connConfig)
	if err != nil {
		return nil, err
	}
	return &emailClient{emailInfo: emailInfo.Decode(),
		connPool:        pool,
		sendTryNum:      sendTryNum,
		sendTryInterval: sendTryInterval,
	}, nil
}

func (e *emailClient) SendEmailNew(data data.Data, toId string, attachments map[string]string, embedFiles *data.EmbedFiles) error {
	//封装Message
	m := gomail.NewMessage()
	if data.SenderInfo != nil && len(data.SenderInfo.Address) > 0 {
		if len(data.SenderInfo.Name) == 0 {
			data.SenderInfo.Name = data.SenderInfo.Address
		}
		m.SetAddressHeader("From", data.SenderInfo.Address, data.SenderInfo.Name)
	} else {
		m.SetHeader("From", m.FormatAddress(e.emailInfo.SenderAddr, e.emailInfo.SenderAddr)) //In this way, you can add aliases, that is, "fromname". If there are special characters such as Chinese, pay attention to character encoding
		if len(e.emailInfo.SenderAddrName) > 0 {
			m.SetHeader("From", m.FormatAddress(e.emailInfo.SenderAddr, e.emailInfo.SenderAddrName)) //In this way, you can add aliases, that is, "fromname". If there are special characters such as Chinese, pay attention to character encoding
		}
	}

	m.SetHeader("To", toId)
	m.SetHeader("Subject", data.Subject)
	m.SetBody("text/html", data.Text)

	if len(attachments) != 0 {
		for fileName, attachment := range attachments {

			m.Attach(attachment, gomail.Rename(fileName), gomail.SetHeader(map[string][]string{
				"Content-Disposition": []string{fmt.Sprintf(`attachment; filename="%s"`, mime.BEncoding.Encode("UTF-8", fileName))},
			}))
		}
	}
	if embedFiles != nil {
		for _, item := range embedFiles.EmbedFileMap {
			m.Embed(fmt.Sprintf("%s/%s", embedFiles.Dir, item))
		}
	}

	/*ical := e.generateIcal(data.Calendar)
	if len(ical) > 0 {
		//log.Debug("generate ical: ", ical)
		m.AddAlternative("text/calendar;method=REQUEST", ical, gomail.SetPartEncoding(gomail.Base64))
	}*/

	var sender EmailSender
	var err error
	for i := 0; i < e.sendTryNum; i++ {
		sender, err = e.connPool.GetSender()
		if err != nil {
			xlog.Warn("get sender failed, err:", xlog.Any("err", err))
			time.Sleep(e.sendTryInterval)
			continue
		}

		err = sender.SendMsg(m)
		if err != nil {
			e.connPool.CloseConn(sender)
			time.Sleep(e.sendTryInterval)
			continue
		}
		e.connPool.PutSender(sender)
		break

	}
	return err
}

/**
 *	sendEmailNew 			邮件发送
 *
 *	@param data				第三方email库需要的data参数
 *	@param toIds			收件人数组
 *
 *	@return error			错误信息
 */
func (e *emailClient) SendEmailByGroup(data data.Data, to, cc, bcc []string, attachments map[string]string, embedFiles *data.EmbedFiles) ([]string, error) {
	m := gomail.NewMessage(gomail.SetEncoding(gomail.Base64))
	if data.SenderInfo != nil && len(data.SenderInfo.Address) > 0 {
		if len(data.SenderInfo.Name) == 0 {
			data.SenderInfo.Name = data.SenderInfo.Address
		}
		m.SetAddressHeader("From", data.SenderInfo.Address, data.SenderInfo.Name)
	} else {
		m.SetHeader("From", m.FormatAddress(e.emailInfo.SenderAddr, e.emailInfo.SenderAddr)) //In this way, you can add aliases, that is, "fromname". If there are special characters such as Chinese, pay attention to character encoding
		if len(e.emailInfo.SenderAddrName) > 0 {
			m.SetHeader("From", m.FormatAddress(e.emailInfo.SenderAddr, e.emailInfo.SenderAddrName)) //In this way, you can add aliases, that is, "fromname". If there are special characters such as Chinese, pay attention to character encoding
		}
	}

	m.SetHeader("To", to...)
	m.SetHeader("Subject", data.Subject) //Set message subject
	m.SetBody("text/html", data.Text)    //Set message body
	if len(cc) > 0 {
		m.SetHeader("Cc", cc...)
	}
	if len(bcc) > 0 {
		m.SetHeader("Bcc", bcc...)
	}

	if len(attachments) != 0 {
		for fileName, attachment := range attachments {
			m.Attach(attachment, gomail.Rename(fileName), gomail.SetHeader(map[string][]string{
				"Content-Disposition": []string{fmt.Sprintf(`attachment; filename="%s"`, mime.BEncoding.Encode("UTF-8", fileName))},
			}))
		}
	}

	if embedFiles != nil {
		for _, item := range embedFiles.EmbedFileMap {
			m.Embed(fmt.Sprintf("%s/%s", embedFiles.Dir, item))
		}
	}
	/*ical := e.generateIcal(data.Calendar)
	if len(ical) > 0 {
		log.Debug("generate ical: ", ical)
		m.AddAlternative("text/calendar;method=REQUEST", ical, gomail.SetPartEncoding(gomail.Base64))
	}*/

	var sender EmailSender
	var err error
	var invalidAddrs []string
	for i := 0; i < e.sendTryNum; i++ {
		sender, err = e.connPool.GetSender()
		if err != nil {
			xlog.Warn("get sender failed, err:", xlog.Any("err", err))
			time.Sleep(e.sendTryInterval)
			continue
		}
		conn := sender.GetConn().(gomail.SendCloser)
		var invalidAddrsArr [][]string
		invalidAddrsArr, err = gomail.SendWithInvalidAddr(conn, m)
		if err != nil {
			xlog.Warn("conn Send fail.", xlog.Any("err", err))
			e.connPool.CloseConn(sender)
			time.Sleep(e.sendTryInterval)
			continue
		}

		if len(invalidAddrsArr) >= 1 {
			//xlog.Warn(fmt.Sprintf("gomail SendWithInvalidAddr contains invalid receiver addr: %v", invalidAddrsArr[0]))
			invalidAddrs = invalidAddrsArr[0]
		}
		e.connPool.PutSender(sender)
		break

	}
	return invalidAddrs, err
}
