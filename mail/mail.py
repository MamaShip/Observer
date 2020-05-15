import os
import smtplib
import sys
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText

MAIL_USERNAME = os.getenv("MAIL_USERNAME", "ObserverZG@gmail.com")
MAIL_PASSWORD = os.getenv("MAIL_PASSWORD", "")

# HOST & PORT
HOST = 'smtp.gmail.com'
PORT = 25

DEFAULT_MSG = '由观察机器人为您寄来的通知邮件'

def send_mail(receiver, contents = DEFAULT_MSG, attachments = []):
    result = True

    sender = MAIL_USERNAME
    # Create SMTP Object
    smtp = smtplib.SMTP()
    print('connecting ...')

    # show the debug log
    smtp.set_debuglevel(1) # TODO

    # connet
    try:
        print(smtp.connect(HOST, PORT))
    except:
        print('CONNECT ERROR ****')
        result = False

    # gmail uses ssl
    smtp.starttls()

    # login with username & password
    try:
        print('loginning ...')
        smtp.login(MAIL_USERNAME, MAIL_PASSWORD)
    except:
        print('LOGIN ERROR ****')
        result = False

    # fill content with MIMEText's object 
    msg = MIMEMultipart()
    
    msg['From'] = sender
    msg['To'] = ';'.join(receiver)
    msg['Subject']='hello , today is a special day.'

    msg.attach(MIMEText(contents, 'plain', 'utf-8'))
    

    if len(attachments) > 0:
        for item in attachments:
            att = MIMEText(open(item, 'rb').read(), 'base64', 'utf-8')
            att["Content-Type"] = 'application/octet-stream'
            f_name = item.split('/')[-1]
            # 这里的filename可以任意写，写什么名字，邮件中显示什么名字
            att["Content-Disposition"] = 'attachment; filename=' + f_name
            msg.attach(att)

    print(msg.as_string())

    try:
        smtp.sendmail(sender, receiver, msg.as_string())
    except smtplib.SMTPException:
        print("Error: 无法发送邮件")
        result = False
    smtp.quit()

    return result


if __name__ == "__main__":
    from_addr = MAIL_USERNAME
    to_addrs=['534440305@qq.com']

    result = send_mail(from_addr, to_addrs, ['/home/twisted/test.txt'])
    print("send mail done, result:", result)
