



class Article_Checker():
    def __init__(self, checker_queue, saving_path='', sleeping_time=1, call_back_func=print):
        super(Article_Checker, self).__init__()
        self.queue = checker_queue
        self.saving_path = saving_path
        self.sleeping_time = sleeping_time
        self.func = call_back_func
        return
    def check_validation(self, URL):
        return True
    def start(self):
        return True
    def get_content(self, URL):
        content = None
        return True, content


class Checker_Queue:
    def put(self, article_id, url, download, block=True, timeout=None):
        pass