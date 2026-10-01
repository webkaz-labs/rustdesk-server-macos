# RustDesk Server for macOS

[日本語](README.md) | [English](README.en.md)

RustDesk OSS Server の `hbbs` / `hbbr` を macOS 向けにビルドし、mise で配布します。追加の小さな Go 製 CLI が、鍵・保存先・ログイン時の自動起動を管理します。RustDesk の公式配布ではありません。

- Apple Silicon 専用のネイティブビルド。指定する最低対応 OS は macOS 15 です。CI のビルド・テスト環境は macOS 26 で、macOS 15 を含むそれ以前の OS での実行互換性は未検証です
- 同梱 upstream: **RustDesk Server 1.1.16**（[固定コミット](https://github.com/rustdesk/rustdesk-server/commit/73523b31cfd25d77dee862e6fc9f5e1fb5e485ef)）
- 利用時の clone、Rust/Go、Homebrew、mise.toml の手編集は不要
- パッケージ署名は GitHub Actions OIDC / Sigstore。Apple Developer ID 署名・公証とは別です

## インストールして起動

**リリース状況:** [v0.1.1](https://github.com/webkaz-labs/rustdesk-server-macos/releases/tag/v0.1.1) を公開済みです。[リリース CI](https://github.com/webkaz-labs/rustdesk-server-macos/actions/runs/36800785882) で macOS 26 / Apple Silicon のネイティブビルド、単体・launchd テスト、署名検証、公開パッケージの mise / Packslip インストールが成功しました。実クライアント間の接続や Tailscale 経由のエンドツーエンド接続は未検証です。

[mise](https://mise.jdx.dev/getting-started.html) を導入・有効化済みの Mac で実行します。Packslip 対応の新しい mise が必要です（CI の固定版は 2026.9.18）。

```sh
mise use -g packslip:github.com/webkaz-labs/rustdesk-server-macos@0.1.1
rustdesk-server setup
```

`setup` はこのリポジトリ独自のコマンドです。クライアントに案内するアドレスと保存先を確認し、最後に `y` で承認するとサービスを登録・起動します。複数の NIC / VPN がある場合は、クライアントから到達できるアドレスを選んでください。

完了時に表示される **ID server / Relay server / Key (PUBLIC)** を RustDesk クライアントの「設定 → ネットワーク → ID/リレーサーバー」に設定します。API server は空欄です。秘密鍵をクライアントへコピーする必要はありません。

```sh
rustdesk-server status  # 状態・クライアント設定を再表示
rustdesk-server stop    # 停止し、次回ログイン時の自動起動も解除
rustdesk-server start   # 起動し、ログイン時の自動起動を復元
```

ログインユーザーの LaunchAgent です。`sudo` は使いません。ログアウト中・ログイン前には動かず、Mac がスリープすると利用できません。

### Tailscale を使う場合

Tailscale は事前にインストール・接続済みで、tailnet のポリシーが接続を許可している必要があります。初回の対話式 `setup` で既存設定も `--address` 指定もない場合だけ、既存の [Tailscale CLI](https://tailscale.com/docs/reference/tailscale-cli?tab=macos) に `status --json --peers=false` で状態を読み取ります。公式 macOS アプリ内の実行ファイルも `TAILSCALE_BE_CLI=1` で利用できます。接続済みの有効な状態を取得できると、LAN / Tailscale / 手入力 / キャンセルから選べます。Tailscale では、この Mac 自身の状態で確認できた `100.x` IP または MagicDNS 名を選びます。

未導入・停止中・不正な状態応答の場合は通常の LAN / 手入力の案内に戻ります。既存設定のアドレスや明示した `--address` は優先され、Tailscale の状態確認は行いません。切り替える場合は、以下のどちらか一方の例を自分の Mac の実際のアドレスに置き換えて実行します。鍵とデータは引き継ぎます。

```sh
# 例: 自分の Mac の Tailscale IP または MagicDNS 名に置き換える
rustdesk-server setup --address 100.100.100.100
# または:
rustdesk-server setup --address my-mac.example-tailnet.ts.net
```

- **全クライアントが、選んだ ID / Relay server のアドレスに到達できる必要があります。** MagicDNS 名にはクライアント側の tailnet DNS 利用も必要です。Tailscale を利用しない Windows クライアントには、到達可能な LAN アドレス、または許可・設定済みの経路が必要です。別のネットワークへの接続経路が自動で追加されることはありません
- 自動インストール・ログイン・ACL・ルーティング・ファイアウォール変更は行いません。Tailscale のアドレスを選んでも daemon の待受けが Tailscale のみに制限されるわけではありません。実際の Tailscale 経由のエンドツーエンド接続は未検証です

### 更新

```sh
mise use -g packslip:github.com/webkaz-labs/rustdesk-server-macos@NEW_VERSION
rustdesk-server setup
```

新しいパッケージの `setup` を実行すると、ランタイムを入れ替えて再起動します。鍵と DB は引き継ぎます。mise の旧バージョンを削除しても、サービスが使うコピーとデータは残ります。旧ランタイムはロールバック確認のため自動削除しません。

## 保存先

既定の場所は `~/Library/Application Support/rustdesk-server/` です。

| パス | 内容 |
| --- | --- |
| `data/` | 秘密鍵 `id_ed25519`、公開鍵 `id_ed25519.pub`、DB |
| `config.json` | アドレス、データ保存先、パッケージ版 |
| `releases/<digest>/` | 検証・コピーした 3 つの実行ファイル |
| `current` | 稼働ランタイムへの安定したリンク |
| `logs/` | `hbbs.log` / `hbbr.log` とエラーログ |

LaunchAgent は `~/Library/LaunchAgents/com.webkaz-labs.rustdesk-server.{hbbs,hbbr}.plist` に作成します。データ・設定・ログは所有者のみアクセス可能にし、秘密鍵は `0600` で保存します。指定したデータディレクトリも `0700` に変更するため、専用の場所を選んでください。

既存セットアップのデータ保存先変更は、誤った鍵の再生成を防ぐため拒否します。バックアップにはデータディレクトリ全体を含め、秘密鍵を公開リポジトリや第三者へ送らないでください。

## ネットワークと安全上の境界

- **LAN アドレスはクライアントに案内するアドレスであり、待受け制限ではありません**。upstream 1.1.16 は全インターフェースの TCP 21115–21119 / UDP 21116 を使用します。`-r` はリレー通知用で、存在しない bind フラグは使用しません
- ファイアウォール、ルーター、ポート転送は変更しません。信頼できる LAN で使い、公開 IP / ポート転送がある環境では意図せずインターネットに到達可能でないか確認してください
- macOS が受信接続・バックグラウンド項目の許可を求める場合があります。内容を確認して判断してください
- `hbbs` と `hbbr` は一つの Ed25519 鍵を使い、双方へ `-k _` を指定します。キーなしリレーは構成しません。公開鍵はユーザー認証の代わりにはならず、クライアント側のアクセス許可・強いパスワードも必要です
- 毎回のサービス起動時にネイティブ CLI が鍵の存在・整合性・権限と `.env` 不在を確認し、既知の最小環境変数で daemon を実行します。鍵が欠けたら停止し、勝手に新しい identity を作りません
- 初回のみ Go の暗号学的乱数で互換鍵を生成します。途中失敗しても生成済み鍵を残し、再実行で再利用します。既存鍵の破損・不一致は上書きしません
- upstream `hbbs` には RustDesk への外向きバージョン確認通信があります
- 同じ macOS ユーザーがファイルを実行中に変更する攻撃に対するサンドボックスではありません

## トラブルシューティング

- **コマンドがない**: mise が現在のシェルで有効か確認します。`mise exec packslip:github.com/webkaz-labs/rustdesk-server-macos@0.1.1 -- rustdesk-server setup` でも実行できます
- **リリース直後に取得できない**: mise の既定の最小リリース経過時間は **24 時間**です。公開後の最初の 24 時間は取得が保留される場合があるため、経過後に再実行してください。レート制限が適用される場合もあります。[Releases](https://github.com/webkaz-labs/rustdesk-server-macos/releases) と [mise Packslip の説明](https://mise.jdx.dev/dev-tools/backends/packslip.html) を確認し、経過時間の保護設定をグローバルに無効化したり、署名検証を無効化したりしないでください
- **macOS が実行をブロックする**: この配布は ad-hoc 署名のみで、公証されていません。署名・取得元を確認して macOS の通常の許可フローを使ってください。Gatekeeper の全体無効化は不要です
- **GUI ログインが必要と表示される**: Mac にログインした本人の Terminal で、`sudo` を付けずに実行します
- **ポートが使用中**: 既存の RustDesk Server / Docker 等と競合していないか調べ、不要な方を止めてから再実行します
- **LAN アドレスが変わった**: `rustdesk-server setup --address NEW_ADDRESS` で更新します。ルーターで DHCP 予約を使うと安定します
- **起動失敗**: `status` と `logs/*.error.log` / `logs/*.log` を確認します。`status` はローカルプロセスの確認であり、クライアントからの接続成功まで保証しません。ログは自動ローテーションしないため容量を時々確認してください
- **鍵が欠落/不一致**: 元のデータをバックアップから復元してください。秘密鍵や公開鍵を片方だけ削除して解決しないでください
- **セットアップ失敗**: 以前の設定・稼働状態への復旧を試みます。復旧にも失敗した場合は明示します。データ・鍵・ステージ済みランタイムは保持されます

自動化する場合だけ、表示される変更内容を承認したうえで `setup --address 192.168.1.20 --data-dir '/absolute/dedicated/path' --yes` を使用できます。

## ビルド・検証・ライセンス

[リリース手順](docs/RELEASING.md) に固定ツールチェーン、ビルド、署名、対応ソースの手順を記載しています。

CLI とパッケージングツールは **Go 1.27.1** でビルドし、Go の標準ライブラリのみを使用します。処理の連携にはシェルスクリプト、upstream のビルドには Rust を使い、このプロジェクト独自の Python ツールはありません。ネイティブ CI は GitHub Actions の **`macos-26`（Apple Silicon / arm64）** を使用します。macOS 15 の最低対応指定は、この CI による macOS 15 での動作確認を意味しません。

```sh
go test -race ./...
go vet ./...
# ネイティブ macOS CI / 使い捨てテスト環境だけ:
RUSTDESK_MACOS_INTEGRATION=1 go test ./internal/service -run TestLaunchdIntegration -count=1 -v
```

通常の Go テストは CLI と Go 製パッケージングツールを対象にし、サービスのテストでは launchctl をモックします。macOS 統合テストは一意のログインユーザー用 label と一時ディレクトリ、ネットワーク待受けのない sleep fixture を使い、終了時に解除します。実際の daemon のクライアント間接続テストは含みません。

upstream は AGPL-3.0、本 CLI は **AGPL-3.0-or-later**（[LICENSE](LICENSE)）。各リリースには upstream の固定コミット・再帰 submodule・ロック済み依存の vendored source・CLI ソース・ビルドスクリプトを含む対応ソースと依存ライセンス一覧を添付します。GitHub Actions の公開ログで、対象コミットに対するネイティブビルド・検証結果を確認できます。
