## 现象
```
GET /api/resources?limit=-1   -> 200（静默忽略负值，返回默认条数）
GET /api/resources?offset=-5  -> 200（同上）
```

对比同类参数已正确校验：
```
GET /api/resources?type=bogus     -> 400 bad_request
GET /api/issues?severity=bogus    -> 400 bad_request
```

## 影响
调用方传入非法分页值时得不到反馈，可能误以为分页生效。
属于 API 契约不一致（枚举参数严格、数值参数宽松）。

## 建议修复方向
`limit`/`offset` 为负时返回 400 `bad_request`，与枚举参数的处理保持一致。

## 验收标准
- [ ] `limit<0` / `offset<0` 返回 400 且消息说明取值范围
- [ ] `limit=0`、超大值（如 99999）的既有行为不变或有明确文档