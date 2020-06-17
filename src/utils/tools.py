import os
import logging

def get_dir_size(dir):
    size = 0
    for root, _, files in os.walk(dir):
        size += sum([os.path.getsize(os.path.join(root, name)) for name in files])
    return size

def total_used_space(dir):
    size = get_dir_size(dir)
    result = ('Total used space is: %.3f MB\n'%(size/1024/1024))
    return result

def str_occupied_space(string):
    return len(string.encode())

def new_logger(name, level=logging.DEBUG):
    #先声明一个 Logger 对象
    logger = logging.getLogger(name)
    logger.setLevel(level=level)
    #然后指定其对应的 Handler 为 FileHandler 对象
    handler = logging.FileHandler(name + '.log')
    #然后 Handler 对象单独指定了 Formatter 对象单独配置输出格式
    formatter = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
    handler.setFormatter(formatter)
    logger.addHandler(handler)
    return logger

def get_logger(name):
    logger = logging.getLogger(name)
    return logger