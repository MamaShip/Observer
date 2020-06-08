from article_checker.update_reason import *

__all__ = ['STATUS_NEW_UNKNOWN', 'STATUS_NORMAL_OB', 'STATUS_NOT_VALID',
            'STATUS_BAD_REQUEST', 'STATUS_INACCESSIBLE', 'STATUS_OUT_OF_DATE',
            'STATUS_DELETE_BY_USER', 'reason2status', 'reason2text']

# article status definition：
#  watching status
STATUS_NEW_UNKNOWN = 0 # 初次添加，状态未知
STATUS_NORMAL_OB = 1 # 正常访问，正常观察状态
STATUS_NOT_VALID = 2 # 不可访问
STATUS_BAD_REQUEST = 3 # 网络异常，初次添加后直接访问失败
#  archive status
STATUS_INACCESSIBLE = 7 # 观察期内链接失效的存档状态
STATUS_OUT_OF_DATE = 8 # 观察超过30天的存档状态
STATUS_DELETE_BY_USER = 9 # 用户主动删除的存档状态

reason2status = {
    REASON_DELETE_BY_USER : STATUS_DELETE_BY_USER,
    REASON_INACCESSIBLE   : STATUS_INACCESSIBLE,
    REASON_OUT_OF_DATE    : STATUS_OUT_OF_DATE,
    REASON_INVALID_URL    : STATUS_NOT_VALID,
    REASON_NULL           : STATUS_NORMAL_OB
}

reason2text = {
    REASON_DELETE_BY_USER : "由用户决定停止观察",
    REASON_INACCESSIBLE   : "已失效，不可访问",
    REASON_OUT_OF_DATE    : "已超出最长观察期",
    REASON_INVALID_URL    : "链接无效，无法观察",
    REASON_NULL           : "停止观察的原因未知"
}