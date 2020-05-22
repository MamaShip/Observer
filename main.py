
import logging
from database.db_operator import DbOperator
from ob import Observer
from my_timer import RepeatedTimer


CMD_LIST = ["status", "list", "admin-status", "admin-list"]
class App(object):
    def __init__(self):
        self.db    = DbOperator()
        self.ob    = Observer()
        self.timer = RepeatedTimer(60, print, "World")

    def __del__(self):
        self.timer.stop()

    def handle_msg(self, msg):
        content = msg.content
        reply = ""

        if self._is_URL(content):
            self._handle_URL(msg)
        elif self._is_email(content):
            self._handle_email(msg)
        elif self._is_cmd(content):
            self._handle_cmd(msg)
        else: # invalid msg
            reply = "听不懂你在说啥！"
        

        return reply

    def _handle_URL(self, msg):
        pass
    def _handle_email(self, msg):
        pass
    def _handle_cmd(self, msg):
        pass

    def _is_URL(self, string):
        return False

    def _is_email(self, string):
        return False

    def _is_cmd(self, string):
        return False
