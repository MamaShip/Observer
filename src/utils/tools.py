import os


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