# USDT 三网络支付 · v0.5.3

支付配置、用户付款选择和付款记录均显示对应的 SVG 网络图标。

| 用户选择 | 网络 | bepusdt `trade_type` |
| --- | --- | --- |
| USDT · TRC20 | TRON | `usdt.trc20` |
| USDT · BEP20 | BNB Smart Chain | `usdt.bep20` |
| USDT · Polygon | Polygon（POL） | `usdt.polygon` |

收款币种均为 USDT。POL 是 Polygon 网络的标识，不会创建 POL 币种订单。参数按 [bepusdt 官方网络文档](https://github.com/v03413/BEpusdt/blob/main/docs/trade-type.md) 对接。

管理员可在「支付配置 → 配置支付网关」勾选开放的网络，至少保留一个。未配置网络列表的旧配置默认支持上述三个网络。对应的钱包、汇率与链上到账检测由现有 bepusdt 网关管理；网站只提交选定网络的固定价格订单。

新付款使用 `usdt_trc20`、`usdt_bep20`、`usdt_polygon` 三个独立方式 ID。旧版 `usdt` 请求仍使用配置默认网络。付款首次创建后保存原网络、网关和加密密钥，不允许通过重复下单改链；调整后台网络设置不会更改已有收银台。旧付款也可通过原保存网络显示图标。只有通过签名及金额校验的到账通知会开通权益。

此次仅升级网站控制服务。原节点内核、Windows 客户端、运营配置及商户凭据不在此次变更范围。
