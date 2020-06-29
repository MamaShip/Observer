import queue
from threading import Lock, Thread
from time import sleep
from mail.mail import send_mail
import logging

logger = logging.getLogger("mail")

# 一个消息队列用在Observer和postman之间
# 消息队列的元素是 (receiver, contents, attachments)
class Mail_Queue:
    _instance = None
    _lock = Lock()

    def __new__(cls, *args, **kwargs):
        if not cls._instance:
            with cls._lock:
                if not cls._instance:
                    cls._instance = super().__new__(cls)
        return cls._instance

    def __init__(self, max_size=50):
        self.q = queue.Queue(maxsize=max_size)
        self._stopped = False

    def put(self, receiver, contents, attachments=[], block=True, timeout=None):
        if self._stopped:
            return False
        
        try:
            self.q.put((receiver, contents, attachments), block=block, timeout=timeout)
        except queue.Full:
            logger.exception("mail queue full")
            return False
        else:
            return True

    def get(self, block=True, timeout=None):
        try:
            (receiver, contents, attachments) = self.q.get(block=block, timeout=timeout)
        except queue.Empty:
            raise queue.Empty
        else:
            return receiver, contents, attachments

    def get_queue_size(self):
        return self.q.qsize()

    def stop(self):
        self._stopped = True

    def is_stopped(self):
        return self._stopped

    def restart(self):
        self._stopped = False



class Postman(Thread):
    _instance = None
    _lock = Lock()

    def __new__(cls, *args, **kwargs):
        if not cls._instance:
            with cls._lock:
                if not cls._instance:
                    cls._instance = super().__new__(cls)
        return cls._instance

    def __init__(self, mail_queue, sleeping_time=30):
        super(Postman, self).__init__()
        self.queue = mail_queue
        self.sleeping_time = sleeping_time
        self._stopped = False
        return
    
    def stop(self):
        self.queue.stop()
        self._stopped = True

    def is_stopped(self):
        return self._stopped

    def run(self):
        while True:
            try:
                (receiver, contents, attachments) = self.queue.get(timeout=1)
            except queue.Empty:
                if self.queue.is_stopped():
                    logger.info("postman job finish stop!")
                    break
            else:
                result = send_mail(receiver, contents, attachments)
                if result != True:
                    logger.error("postman send mail to " + str(receiver) + "fail")

            sleep(self.sleeping_time)
