from article_checker.update_reason import *

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