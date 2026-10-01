// SPDX-License-Identifier: AGPL-3.0-or-later
package service

// Translate message templates only. Arguments such as paths, addresses, keys,
// command output and system diagnostics retain their original values.
var japaneseMessages = map[string]string{
	usage: `rustdesk-server: RustDesk Server macOS ユーザーサービス管理ツール

使い方:
  rustdesk-server [--lang auto|ja|en] setup [--address HOST] [--data-dir ABSOLUTE_PATH] [--yes]
  rustdesk-server start
  rustdesk-server stop
  rustdesk-server status
  rustdesk-server version

setup はログイン時に起動するサービスの登録・起動前に確認します。--yes には明示的な
--address の指定が必要で、表示された計画を承認します。stop は次回ログイン時の自動起動も無効にし、
start は再び有効にします。sudo の使用やルーター・ファイアウォールの設定変更は行いません。
--lang の既定値は auto です。LC_ALL、LC_MESSAGES、LANG、macOS の優先言語の順で判定します。
日本語のロケールでは日本語、それ以外または不明なロケールでは英語を使用します。
対話形式の setup では LAN または接続済みの Tailscale アドレスを選択できます。すべてのクライアントから
選択したアドレスへ到達できる必要があります。Tailscale のインストールや設定変更は行いません。
`,
	setupUsage: `使い方: rustdesk-server [--lang auto|ja|en] setup [options]

  --address HOST          すべてのクライアントから到達できる IP またはホスト名（LAN または Tailscale）
  --data-dir ABSOLUTE_PATH 永続データディレクトリ（専用・所有者のみアクセス可能）
  --yes                   表示された計画を承認（--address が必要）
  --lang auto|ja|en        表示言語（既定値: auto）
  --help                  このヘルプを表示

--yes を指定しない場合は、アドレス、データディレクトリ、ネットワークへの公開範囲を確認してから
y で承認してください。質問への回答に cancel と入力すると、サービスを変更せず終了します。
sudo の使用やルーター・ファイアウォール・Tailscale の設定変更は行いません。
`,
	clientReachabilityNotice: "すべてのクライアントから、選択した ID/リレーサーバーのアドレスへ到達できる必要があります。Tailscale アドレスには tailnet へのアクセスと\nRustDesk のポートを許可するポリシーが必要です。MagicDNS にはクライアント側で正常に動作する DNS も必要です。\nTailscale を利用していない Windows から Tailscale の 100.x アドレスへ直接接続することはできません。到達可能な\nLAN アドレスまたは別途承認されたネットワーク経路を使用してください。setup はいずれの経路も設定しません。",

	"help accepts only an optional command: rustdesk-server help setup": "help に指定できる追加引数は setup のみです: rustdesk-server help setup",
	"%s accepts no arguments": "%s は引数を受け付けません",
	"internal service invocation requires state directory and daemon name":                     "内部サービスの起動には状態ディレクトリとデーモン名が必要です",
	"unknown command %q; run rustdesk-server help":                                             "不明なコマンド %q です。rustdesk-server help を実行してください",
	"service management requires macOS; this command made no service changes":                  "サービスの管理には macOS が必要です。このコマンドによるサービスの変更はありません",
	"run as the logged-in user without sudo; system-wide/pre-login services are not supported": "sudo を使わず、ログイン中のユーザーとして実行してください。システム全体またはログイン前に動作するサービスはサポートしていません",
	"could not find the home directory; run from your normal login session: %w":                "ホームディレクトリを取得できませんでした。通常のログインセッションから実行してください: %w",
	"Started; will start automatically at login.":                                              "起動しました。ログイン時にも自動起動します。",
	"Stopped; automatic start at login disabled. Data and keys kept.":                          "停止しました。ログイン時の自動起動も無効にしました。データと鍵は保持されています。",
	"unexpected argument %q; run rustdesk-server setup --help":                                 "予期しない引数 %q です。rustdesk-server setup --help を実行してください",
	"--%s does not accept a value":                                                             "--%s に値は指定できません",
	"invalid value %q for --yes; use true or false":                                            "--yes の値 %q は無効です。true または false を指定してください",
	"--%s requires a value; run rustdesk-server setup --help":                                  "--%s には値が必要です。rustdesk-server setup --help を実行してください",
	"unknown option %q; run rustdesk-server setup --help":                                      "不明なオプション %q です。rustdesk-server setup --help を実行してください",
	"--yes requires --address; address detection must be reviewed":                             "--yes には --address の指定が必要です。検出されたアドレスは確認が必要です",
	"setup cancelled":             "セットアップを中止しました",
	"Cancelled; no changes made.": "中止しました。変更はありません。",
	"Keeping configured client-facing address %s; use setup --address HOST to change it.\n": "設定済みのクライアント接続先アドレス %s を保持します。変更するには setup --address HOST を実行してください。\n",
	"Persistent data directory": "永続データディレクトリ",
	"existing data directory cannot be changed by setup; keep the current path and back it up before manual migration":                                        "既存のデータディレクトリは setup では変更できません。現在のパスを維持し、手動で移行する前にバックアップしてください",
	"\nSetup plan\n  Client ID server: %s\n  Relay server: %s\n  Persistent data: %s (owner-only permissions)\n  Runtime/logs: %s\n  User LaunchAgents: %s\n": "\nセットアップ計画\n  クライアント用 ID サーバー: %s\n  リレーサーバー: %s\n  永続データ: %s（所有者のみアクセス可能）\n  ランタイム/ログ: %s\n  ユーザー用 LaunchAgents: %s\n",
	"  Creates or preserves one Ed25519 key pair; copies the bundled hbbs/hbbr.\n  Starts hbbs/hbbr now and on this user's login; an existing setup is restarted.\n  Opens TCP 21115–21119 and UDP 21116 on ALL network interfaces.\n  This is not a LAN-only bind. Use a trusted LAN; no router/firewall settings change.\n  macOS may ask you to allow incoming connections/background items.\n  Upstream hbbs performs its own outbound version check to RustDesk.\n  Server key identifies the server; use strong RustDesk client access passwords.": "  Ed25519 鍵ペアを 1 組作成または保持し、同梱の hbbs/hbbr をコピーします。\n  hbbs/hbbr を今すぐ起動し、このユーザーのログイン時にも起動します。既存のセットアップがある場合はサービスを再起動します。\n  すべてのネットワークインターフェースで TCP 21115–21119 と UDP 21116 を待ち受けます。\n  LAN 限定の待ち受けではありません。信頼できる LAN を使用してください。ルーター・ファイアウォールの設定は変更しません。\n  macOS から受信接続やバックグラウンド項目の許可を求められる場合があります。\n  上流版 hbbs は、RustDesk への外向き通信で独自にバージョン確認を行います。\n  サーバー鍵はサーバーの識別に使われます。RustDesk クライアントのアクセス用パスワードには強力なものを使用してください。",
	"Apply and start? [y/N]": "適用して起動しますか？ [y/N]",
	"\nSetup complete. Existing data and server identity are kept on later setup runs.": "\nセットアップが完了しました。今後 setup を再実行しても、既存のデータとサーバー識別情報は保持されます。",
	"LAN address detection unavailable; you can enter an address manually.":             "LAN アドレスを検出できませんでした。アドレスを手動で入力できます。",
	"Detected private LAN IPv4 addresses:":                                              "検出された LAN 内のプライベート IPv4 アドレス:",
	"  %s (%s)\n":                                                                       "  %s (%s)\n",
	"Client-facing address":                                                             "クライアントの接続先アドレス",
	"Connected Tailscale addresses for this Mac:":                                       "この Mac の接続済み Tailscale アドレス:",
	"Client network (lan/tailscale/manual/cancel)":                                      "クライアントのネットワーク (lan/tailscale/manual/cancel)",
	"Client-facing LAN address":                                                         "クライアントの接続先 LAN アドレス",
	"Client-facing Tailscale IP or MagicDNS hostname":                                   "クライアントの接続先 Tailscale IP または MagicDNS ホスト名",
	"Choose lan, tailscale, manual, or cancel.":                                         "lan、tailscale、manual、cancel のいずれかを選択してください。",
	"input ended before confirmation; no service changes made (use explicit flags and --yes for automation)": "確認前に入力が終了しました。サービスは変更していません（自動化する場合は必要なフラグを明示し、--yes を指定してください）",
	"choose a dedicated data subdirectory":                               "データ専用のサブディレクトリを指定してください",
	"data must be outside managed runtime and log directories":           "データは管理対象のランタイムおよびログのディレクトリ外に配置してください",
	"address must be a reachable IP or hostname, without scheme or port": "到達可能な IP またはホスト名を、スキームやポート番号を付けずに指定してください",
	"choose a client-reachable non-loopback address":                     "クライアントから到達可能な、ループバック以外のアドレスを指定してください",
	"provide only a hostname/IP, not a port or URL":                      "ポート番号や URL ではなく、ホスト名または IP のみを指定してください",
	"invalid hostname":                           "ホスト名が無効です",
	"Not configured. Run rustdesk-server setup.": "未設定です。rustdesk-server setup を実行してください。",
	"%s: %s\n": "%s: %s\n",
	"\nRustDesk client settings → Network → ID/Relay server\n  ID server: %s\n  Relay server: %s\n  Key (PUBLIC): %s\n  API server: leave blank\n\nData: %s\nLogs: %s\nPackage: %s\n": "\nRustDesk クライアントの設定 → ネットワーク → ID/リレーサーバー\n  ID サーバー: %s\n  リレーサーバー: %s\n  キー（公開鍵）: %s\n  API サーバー: 空欄のままにしてください\n\nデータ: %s\nログ: %s\nパッケージ: %s\n",
	"Status checks local launchd processes, not end-to-end remote connectivity.\nIf DHCP/VPN changes the reachable address, rerun setup --address NEW_ADDRESS.":                       "status はローカルの launchd プロセスを確認します。リモートからのエンドツーエンドの接続確認は行いません。\nDHCP/VPN によって到達可能なアドレスが変わった場合は、setup --address NEW_ADDRESS を再実行してください。",
	"one or more services are stopped; run start, or inspect the logs": "停止しているサービスがあります。start を実行するか、ログを確認してください",

	"use an absolute path without control characters": "制御文字を含まない絶対パスを指定してください",
	"refusing non-regular file: %s":                   "通常ファイルではないため使用を拒否しました: %s",
	"refusing non-directory or symlink: %s":           "ディレクトリではないか、シンボリックリンクであるため使用を拒否しました: %s",
	"directory is not owned by this user: %s":         "このユーザーが所有するディレクトリではありません: %s",
	"refusing non-regular destination: %s":            "出力先が通常ファイルではないため書き込みを拒否しました: %s",
	"public key exists without private key; restore the private key from backup, do not rotate silently": "公開鍵はありますが秘密鍵がありません。バックアップから秘密鍵を復元してください。確認せずに鍵を作り直さないでください",
	"could not generate server key":                                               "サーバー鍵を生成できませんでした",
	"could not persist private key; inspect data directory before retrying":       "秘密鍵を保存できませんでした。再試行する前にデータディレクトリを確認してください",
	"invalid private key; restore a valid backup (existing key was not replaced)": "秘密鍵が無効です。有効なバックアップから復元してください（既存の鍵は置き換えていません）",
	"private key failed Ed25519 consistency check (not replaced)":                 "秘密鍵の Ed25519 整合性チェックに失敗しました（鍵は置き換えていません）",
	"public/private key mismatch; restore the matching pair (not replaced)":       "公開鍵と秘密鍵が一致しません。対応する鍵ペアを復元してください（鍵は置き換えていません）",
	"invalid public key file":                                                     "公開鍵ファイルが無効です",
	"invalid config.json":                                                         "config.json が無効です",
	"invalid trailing data in config.json":                                        "config.json の末尾に不正なデータがあります",
	"unsupported config schema":                                                   "サポートされていない設定スキーマです",
	"data_dir must be a canonical absolute path":                                  "data_dir には正規化された絶対パスを指定してください",
	"find bundled %s next to rustdesk-server: %w":                                 "rustdesk-server と同じディレクトリにある同梱の %s を確認してください: %w",
	"not executable: %s":                                                          "実行できません: %s",
	"cached runtime is not a real directory":                                      "キャッシュされたランタイムが実体のあるディレクトリではありません",
	"cached runtime is not executable":                                            "キャッシュされたランタイムを実行できません",
	"cached runtime checksum mismatch":                                            "キャッシュされたランタイムのチェックサムが一致しません",
	"package changed during setup; retry after installation completes":            "セットアップ中にパッケージが変更されました。インストールの完了後に再試行してください",
	"current runtime path is not a symlink":                                       "現在のランタイムのパスがシンボリックリンクではありません",
	"unsafe setup lock":                                                           "セットアップ用ロックの安全性を確認できません",
	"another rustdesk-server operation is running":                                "別の rustdesk-server 操作が実行中です",

	"launchd service is not loaded": "launchd にサービスが読み込まれていません",
	"launchctl %s: %w (%s)":         "launchctl %s の実行に失敗しました: %w (%s)",
	"a macOS graphical login session is required; run this in Terminal as the logged-in user, without sudo":                                 "macOS のグラフィカルログインセッションが必要です。sudo を使わず、ログイン中のユーザーとしてターミナルで実行してください",
	"refusing to replace an unmanaged LaunchAgent: %s":                                                                                      "管理対象外の LaunchAgent の置き換えを拒否しました: %s",
	"loaded service has no managed plist: %s":                                                                                               "読み込み済みのサービスに管理対象の plist がありません: %s",
	"data directory differs from existing setup; keep it unchanged to preserve server identity (manual migration requires a backup)":        "データディレクトリが既存の設定と異なります。サーバー識別情報を保持するため、変更しないでください（手動で移行する場合はバックアップが必要です）",
	"existing setup private key is missing; restore a backup before setup (identity will not be regenerated)":                               "既存の設定の秘密鍵が見つかりません。setup の前にバックアップを復元してください（サーバー識別情報は再生成しません）",
	"managed data directory contains .env; remove or migrate it before setup because upstream environment overrides can disable key checks": "管理対象のデータディレクトリに .env があります。上流版の環境設定の上書きにより鍵の検証が無効になる可能性があるため、setup の前に削除または移行してください",
	"LaunchAgents directory is invalid":                                                                                                     "LaunchAgents ディレクトリが無効です",
	"%w; rollback also failed: %v; inspect status before retrying":                                                                          "%w。元の状態への復元にも失敗しました: %v。再試行する前に status を確認してください",
	"%w; previous service configuration restored; data and keys preserved":                                                                  "%w。以前のサービス設定を復元しました。データと鍵は保持されています",
	"run rustdesk-server setup first: %w":                                                                                                   "まず rustdesk-server setup を実行してください: %w",
	"private key missing; restore a backup before start":                                                                                    "秘密鍵が見つかりません。start の前にバックアップを復元してください",
	"managed data directory contains .env; refusing to start":                                                                               "管理対象のデータディレクトリに .env があるため、起動を拒否しました",
	"runtime missing; rerun setup: %w":                                                                                                      "ランタイムが見つかりません。setup を再実行してください: %w",
	"runtime %s is not executable; rerun setup":                                                                                             "ランタイム %s を実行できません。setup を再実行してください",
	"TCP port %d is unavailable; stop the other server first":                                                                               "TCP ポート %d を使用できません。先に別のサーバーを停止してください",
	"UDP port 21116 is unavailable":                                                                                                         "UDP ポート 21116 を使用できません",
	"services did not become ready; inspect logs under %s":                                                                                  "サービスの起動準備が完了しませんでした。次のディレクトリ内のログを確認してください: %s",

	"--lang requires ja, en, or auto; run rustdesk-server help": "--lang には ja、en、auto のいずれかが必要です。rustdesk-server help を実行してください",
	"invalid --lang value %q; choose ja, en, or auto":           "--lang の値 %q は無効です。ja、en、auto のいずれかを選択してください",
	"System detail: %s": "システム詳細: %s",
	"Operation failed. Review the diagnostic below; check rustdesk-server status and the logs before retrying.\nSystem detail: %s": "操作に失敗しました。以下の診断内容を確認し、再試行する前に rustdesk-server status とログを確認してください。\nシステム詳細: %s",
	"Error: ": "エラー: ",

	"data path is not a real directory":                                          "データのパスが実体のあるディレクトリではありません",
	"data directory must have owner-only permissions (0700)":                     "データディレクトリのアクセス権は所有者のみ（0700）にしてください",
	"data directory has a different owner":                                       "データディレクトリの所有者が異なります",
	".env overrides are not allowed in managed data":                             "管理対象のデータでは .env による設定の上書きは許可されていません",
	"private key missing/unreadable; restore a backup, no replacement generated": "秘密鍵が見つからないか、読み取れません。バックアップを復元してください。代わりの鍵は生成していません",
	"private key must have owner-only permissions (0600)":                        "秘密鍵のアクセス権は所有者のみ（0600）にしてください",
	"private key unreadable; no replacement generated":                           "秘密鍵を読み取れません。代わりの鍵は生成していません",
	"private key invalid; no replacement generated":                              "秘密鍵が無効です。代わりの鍵は生成していません",
	"private key consistency check failed":                                       "秘密鍵の整合性チェックに失敗しました",
	"public/private key mismatch":                                                "公開鍵と秘密鍵が一致しません",
	"invalid internal daemon name":                                               "内部デーモン名が無効です",
	"managed executable missing or not executable":                               "管理対象の実行ファイルが見つからないか、実行できません",
	"exec %s: %w": "%s の実行に失敗しました: %w",

	"stopped":                                "停止中",
	"loaded":                                 "読み込み済み",
	"not running":                            "未実行",
	"loaded, not running":                    "読み込み済み・未実行",
	"running":                                "実行中",
	"Tailscale IPv4":                         "Tailscale IPv4",
	"Tailscale IPv6":                         "Tailscale IPv6",
	"MagicDNS hostname":                      "MagicDNS ホスト名",
	"MagicDNS hostname; requires client DNS": "MagicDNS ホスト名（クライアント側の DNS が必要）",
}
