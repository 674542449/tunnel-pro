# 支付网关接入 v0.5.2

管理后台新增「支付配置」页面，地址为 https://test.xiaguamail.com/control/#paymentconfig 。该页仅管理员可访问，沿用后台二次验证和 CSRF 检查。

## 经营方填写的配置

易支付：HTTPS 网关基础地址、商户号、商户密钥，以及支付宝 / 微信通道开关。本版对接常见的易支付 V1 MD5 协议，生成签名后的 `submit.php` 收银台地址。地址可以包含网关自身的路径前缀，但不要附带 `submit.php`、查询参数或密钥。需要 RSA 的易支付 V2 不属于此适配器。

bepusdt：HTTPS 网关基础地址、API Token 和 USDT 网络类型，默认 `usdt.trc20`；也可以填写网关支持的 `usdt.erc20`、`usdt.bep20` 等。钱包地址、汇率、链上扫描和收款网络在已有 bepusdt 实例中管理。

密钥使用现有安全密钥进行 AES-256-GCM 加密后保存到 PostgreSQL。配置读取只返回「是否已保存」，编辑时不回显密钥，留空保留原值。每笔付款保留创建时的商户与加密密钥，后续修改渠道不会让旧订单失去回调验签能力。付款与账单 API 不返回这份私密快照。

## 订单与到账

支付模式为正式渠道时，用户可选择支付宝、微信或 USDT；易支付限定人民币套餐，bepusdt 使用套餐的 CNY / USD / EUR 法币金额计算应付 USDT。请求金额始终来自服务器的订单快照，客户端不能指定金额。

- 易支付通知：`GET` / 表单 `POST /api/payment/webhook/epay`，验证 MD5、商户号、通道、商户订单号、平台流水、金额和 `TRADE_SUCCESS`；提交成功后返回纯文本 `success`。
- bepusdt 下单：`POST /api/v1/order/create-transaction`，传入正数法币金额、固定 USDT 网络及商户订单号。网关响应必须与订单号、法币和金额一致，收银台必须属于配置的网关。
- bepusdt 通知：JSON `POST /api/payment/webhook/bepusdt`，验证原生签名、商户订单号、平台交易号与法币金额。状态 1 只表示等待，状态 3 表示到期，状态 2 才确认付款；成功提交后返回 `ok`。
- 浏览器返回 `#orders` 不确认付款。重复通知通过原有付款事件和购买权益约束只开通一次。已退款订单收到旧付款通知不会恢复权益。
- 已过期或取消订单收到有效的实际到账通知时入账并发放权益，同时产生对账告警。

同一订单保持一种付款方式。USDT 收银台已创建时复用原链接；bepusdt 原生接口会重建同一个商户订单号，因此请求结果不确定时不自动重试，保留付款并记录告警。后台重启时中断的创建也进入待核实状态，经营方需在 bepusdt 核实对应商户订单号；有效到账回调仍可正常处理。

## 退款与开售状态

此版不调用易支付各分支不一致的退款 API，也不从 USDT 钱包发起转账。经营方先在支付平台或钱包完成整笔退款，再在「付款与退款」点击「登记已完成退款」，填写退款原因及渠道流水号 / 链上交易哈希。保存后撤销权益，保留操作者和审计记录；重复登记不会重复退款记账。Stripe 原有接口退款保持兼容。

部署后仍保持模拟付款模式，没有填写真实商户密钥，也没有实际扣款。保存正式渠道配置不绕过原有开售条件：实际邮件与告警、管理员二次验证、正式计费节点、备份及正式开售批准等仍需满足；未满足时收银台创建返回明确提示。「运维告警」页列出缺项。服务器商业配置允许 `payment_mode=gateways`，正式开售开关仍由私密服务器配置管理。

## 验收与来源

验收使用模拟网关，不调用真实支付、钱包或 SMTP 服务。报告包括 `admin-payments-gateway-v0.5.2.json`（隔离 PostgreSQL 与真实 HTTP API）、`admin-commerce-dom-v0.5.2.json`（配置表单和现有购买流程）、`admin-mfa-dom-v0.5.2.json`（强制二次验证下所有页面），以及部署后的 `admin-payment-deployment-v0.5.2.json`。

协议参考：[易支付 V1 文档](https://epay.uxw.net/doc/v1_legacy_api.html)、[bepusdt 原生 API](https://github.com/v03413/BEpusdt/blob/main/docs/api/api.md)、[bepusdt 回调说明](https://github.com/v03413/BEpusdt/blob/main/docs/notify/readme.md)。核对的 bepusdt 参考提交为 `aa3bd5097f258cccd5a53baed7211c74733b8332`，接口实现独立编写，没有将网关源码或钱包程序嵌入 tunnelX。

此次更新仅管理后台；Windows 客户端继续使用 v0.5.0，现有节点内核及连接不需升级。
