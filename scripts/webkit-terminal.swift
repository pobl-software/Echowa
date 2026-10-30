// Opens a custom app's Terminal tab in WebKit, the engine Safari uses, and
// prints what its WebSocket did and any CSP violation. Signs in with the
// password on stdin, waits idle seconds (0 by default), then runs a command
// through the socket (docs/verification.md, "Terminal"). Exits 1 unless the
// command's output came back with no CSP violation.
//
//   printf '%s\n' "$PASSWORD" | swift scripts/webkit-terminal.swift <origin> <email> <app id> [idle]
//
// Point it at a release build: the CSP is off while Vite's dev server runs,
// and the dev pages don't render in a hidden WebKit view. It trusts any
// certificate on localhost, such as Traefik's default one.
import Cocoa
import WebKit

let args = CommandLine.arguments
guard args.count == 4 || args.count == 5, let password = readLine() else {
    print("usage: printf '%s\\n' \"$PASSWORD\" | swift webkit-terminal.swift <origin> <email> <app id> [idle seconds]")
    exit(2)
}
let origin = args[1], email = args[2], appID = args[3]
let idle = args.count == 5 ? Double(args[4]) ?? 0 : 0

_ = NSApplication.shared
NSApp.setActivationPolicy(.prohibited)

// Runs before the page's own scripts, so it sees every socket.
let probe = """
window.__log = [];
document.addEventListener('securitypolicyviolation', (e) => window.__log.push('csp ' + e.violatedDirective + ' ' + e.blockedURI));
const Native = window.WebSocket;
window.WebSocket = class extends Native {
  constructor(url, protocols) {
    super(url, protocols);
    window.__ws = this;
    window.__log.push('new ' + url);
    this.addEventListener('open', () => window.__log.push('open'));
    this.addEventListener('error', () => window.__log.push('error'));
    this.addEventListener('close', (e) => window.__log.push('close ' + e.code + ' after ' + Math.round(performance.now() / 1000) + 's'));
    this.addEventListener('message', (e) => window.__log.push(typeof e.data === 'string' ? 'text ' + e.data : 'bytes ' + new TextDecoder().decode(e.data)));
  }
};
"""

final class Navigation: NSObject, WKNavigationDelegate {
    var loaded = false
    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) { loaded = true }
    func webView(_ webView: WKWebView, didReceive challenge: URLAuthenticationChallenge,
                 completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        if challenge.protectionSpace.host == "localhost", let trust = challenge.protectionSpace.serverTrust {
            completionHandler(.useCredential, URLCredential(trust: trust))
        } else {
            completionHandler(.performDefaultHandling, nil)
        }
    }
}

let config = WKWebViewConfiguration()
config.websiteDataStore = .nonPersistent()
config.userContentController.addUserScript(WKUserScript(source: probe, injectionTime: .atDocumentStart, forMainFrameOnly: true))
let web = WKWebView(frame: NSRect(x: 0, y: 0, width: 1280, height: 900), configuration: config)
let navigation = Navigation()
web.navigationDelegate = navigation

func pump(_ seconds: Double) { RunLoop.main.run(until: Date().addingTimeInterval(seconds)) }

func load(_ path: String) {
    navigation.loaded = false
    web.load(URLRequest(url: URL(string: origin + path)!))
    let deadline = Date().addingTimeInterval(15)
    while !navigation.loaded && Date() < deadline { pump(0.05) }
}

func js(_ code: String) -> String {
    var out = "", done = false
    web.evaluateJavaScript(code) { result, error in
        out = error.map { "error: \($0)" } ?? "\(result ?? "nil")"
        done = true
    }
    while !done { pump(0.05) }
    return out
}

// Inertia renders after load, so wait for what the next step needs.
func waitFor(_ condition: String) {
    let deadline = Date().addingTimeInterval(15)
    while js("!!(\(condition))") != "1" && Date() < deadline { pump(0.2) }
}

// React reads a controlled input's value from its input event.
let signIn = """
(() => {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
  for (const [selector, value] of [['input[type=email]', \(String(reflecting: email))], ['input[type=password]', \(String(reflecting: password))]]) {
    const input = document.querySelector(selector);
    set.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  }
  document.querySelector('button[type=submit]').click();
  return 'signed in';
})()
"""

print(js("navigator.userAgent"))
load("/login")
waitFor("document.querySelector('input[type=password]')")
print(js(signIn))
pump(3)
load("/apps/\(appID)?tab=terminal")
waitFor("window.__log.some((l) => l.startsWith('text '))")
if idle > 0 {
    print("idle for \(Int(idle))s")
    pump(idle)
}
// The shell works out 1+1, so the reply can't be the typed command's echo.
print(js("window.__ws ? (window.__ws.send(new TextEncoder().encode('echo webkit-$((1+1))-ok\\r')), 'sent: echo webkit-$((1+1))-ok') : 'no socket'"))
pump(2)
let log = js("window.__log.map((l) => l.replace(/\\x1b\\[[0-9;?]*[a-zA-Z]/g, '').trim().slice(0, 120)).filter(Boolean).join('\\n')")
print(log)
let passed = log.contains("webkit-2-ok") && !log.contains("csp ")
print(passed ? "PASS" : "FAIL")
exit(passed ? 0 : 1)
