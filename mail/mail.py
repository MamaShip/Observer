import os
import smtplib
import sys
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText
import logging

#先声明一个 Logger 对象
logger = logging.getLogger("mail")
logger.setLevel(level=logging.DEBUG)
#然后指定其对应的 Handler 为 FileHandler 对象
handler = logging.FileHandler('mail.log')
#然后 Handler 对象单独指定了 Formatter 对象单独配置输出格式
formatter = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
handler.setFormatter(formatter)
logger.addHandler(handler)

MAIL_USERNAME = os.getenv("MAIL_USERNAME", "ObserverZG@gmail.com")
MAIL_PASSWORD = os.getenv("MAIL_PASSWORD", "")

# HOST & PORT
HOST = 'smtp.gmail.com'
PORT = 25

DEFAULT_MSG = {'Subject': '您的观察目标有状态更新',
               'Body': '由观察机器人为您寄来的通知邮件'}


def send_mail(receiver, contents=DEFAULT_MSG, attachments=[]):
    """Send Email to user.
    this function calls Linux service 'sendmail' to use Gmail smtp service.

    Args:
        receiver: a string of receiver's email address
        contents: a dict contains values of {'Subject', 'Body'}
        attachments: a list of strings. Path of the file to be attached.

    Returns:
        success: True/False - if any error happened,
                this will be False. 
    """
    result = True

    sender = MAIL_USERNAME
    # Create SMTP Object
    smtp = smtplib.SMTP()
    print('connecting ...')
    # show the debug log
    # smtp.set_debuglevel(2)

    # connet
    try:
        print(smtp.connect(HOST, PORT))
    except:
        print('CONNECT ERROR ****')
        logger.error("Fail to connect")
        result = False

    # gmail uses ssl
    smtp.starttls()

    # login with username & password
    try:
        print('loginning ...')
        smtp.login(MAIL_USERNAME, MAIL_PASSWORD)
    except Exception as e:
        print('LOGIN ERROR ****')
        # log it (user password is not allowed to be recorded in log file)
        logger.error("smtp login fail with username:" + MAIL_USERNAME)
        logger.error("> %s\n" % (e))
        result = False

    # fill content with MIMEText's object
    msg = MIMEMultipart()

    msg['From'] = sender
    msg['To'] = ';'.join(receiver)
    msg['Subject'] = contents['Subject']
    msg.attach(MIMEText(contents['Body'], 'plain', 'utf-8'))

    # add attachments
    if len(attachments) > 0:
        for item in attachments:
            att = MIMEText(open(item, 'rb').read(), 'base64', 'utf-8')
            att["Content-Type"] = 'application/octet-stream'
            f_name = item.split('/')[-1]
            # 这里的filename可以任意写，写什么名字，邮件中显示什么名字
            att["Content-Disposition"] = 'attachment; filename=' + f_name
            msg.attach(att)

    # print(msg.as_string())
    try:
        smtp.sendmail(sender, receiver, msg.as_string())
        logger.info("mail sent to %s" % (msg['To']))
    except smtplib.SMTPException as e:
        print("Error: 无法发送邮件")
        logger.error("sendmail FAIL. reason: %s " % (e))
        result = False
    smtp.quit()

    return result


if __name__ == "__main__":
    to_addrs = ['534440305@qq.com']
    result = send_mail(to_addrs, attachments=['/home/twisted/test.txt'])
    print("send mail done, result:", result)
