import { useState } from 'react';
import { Alert, Button, Card, Input, Space, Typography } from 'antd';
import { api } from '../api';
import type { JoinResult } from '../types';

// Public join page: the landing target of a pairing QR
// (<origin>/join#<token>) and of hand-typed short codes. The token lives
// in the URL fragment, so it never reaches the server's logs; redemption
// creates the device's account and logs this browser straight in. The
// generated password is shown once here — we also offer it to the
// browser's password manager (Credential Management), best-effort.
export default function JoinPage() {
  const [token] = useState(() => decodeURIComponent(window.location.hash.replace(/^#/, '')));
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [res, setRes] = useState<JoinResult | null>(null);
  const [savedPw, setSavedPw] = useState(false);

  const join = async (body: Record<string, string>) => {
    setBusy(true);
    setErr('');
    try {
      const r = await api<JoinResult>('/api/v1/join', { method: 'POST', body: JSON.stringify(body) });
      setRes(r);
      offerPassword(r);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  // Password Credential Management support is patchy; a failure here must
  // never block the join, which is why everything is wrapped and optional.
  const offerPassword = async (r: JoinResult) => {
    try {
      const PW = (window as unknown as { PasswordCredential?: new (init: { id: string; name: string; password: string }) => void })
        .PasswordCredential;
      if (typeof PW === 'function' && 'credentials' in navigator) {
        await navigator.credentials.create({
          password: new PW({ id: r.user.username, name: r.user.username, password: r.password }),
        } as CredentialCreationOptions);
        setSavedPw(true);
      }
    } catch {
      /* the password stays on screen regardless */
    }
  };

  return (
    <div style={{ display: 'flex', justifyContent: 'center', marginTop: '10vh' }}>
      <Card title="Join this file server" style={{ width: 420, maxWidth: '92vw' }}>
        {res ? (
          <Space direction="vertical" size={12} style={{ width: '100%' }}>
            <Alert
              type="success"
              showIcon
              message={`Joined as ${res.user.tenant}/${res.user.username}`}
              description="You are logged in on this device already."
            />
            <div>
              <Typography.Text strong>username</Typography.Text>
              <Typography.Paragraph copyable={{ text: res.user.username }} className="mono">
                {res.user.username}
              </Typography.Paragraph>
              <Typography.Text strong>password</Typography.Text>
              <Typography.Paragraph copyable={{ text: res.password }} className="mono">
                {res.password}
              </Typography.Paragraph>
              {res.apikey && (
                <>
                  <Typography.Text strong>API key</Typography.Text>
                  <Typography.Paragraph copyable={{ text: res.apikey }} className="mono">
                    {res.apikey}
                  </Typography.Paragraph>
                </>
              )}
            </div>
            {savedPw ? (
              <Typography.Text type="success">
                The password was offered to this browser's password manager.
              </Typography.Text>
            ) : (
              <Typography.Text type="warning">
                Copy the password somewhere safe — it is shown only once.
              </Typography.Text>
            )}
            <Button type="primary" block onClick={() => (window.location.href = '/')}>
              Continue to files
            </Button>
          </Space>
        ) : (
          <Space direction="vertical" size={12} style={{ width: '100%' }}>
            <Typography.Paragraph>
              Awaiting a pairing invite. The invite's owner (a tenant admin) opened it on the
              Pairings tab; it is valid only for a few minutes and only once.
            </Typography.Paragraph>
            {token ? (
              <Button type="primary" block loading={busy} onClick={() => join({ token })}>
                Join with the scanned invite
              </Button>
            ) : (
              <Space.Compact style={{ width: '100%' }}>
                <Input
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  onPressEnter={() => code && join({ code })}
                  placeholder="type the 7-character code"
                  autoFocus
                  maxLength={7}
                />
                <Button type="primary" loading={busy} onClick={() => code && join({ code })}>
                  Join
                </Button>
              </Space.Compact>
            )}
            {err && (
              <Alert
                type="error"
                showIcon
                message={err}
                description={
                  token
                    ? 'If the scan link stopped working, ask for a new invite — or type its 7-character code below.'
                    : undefined
                }
              />
            )}
            {token && (
              <Space.Compact style={{ width: '100%' }}>
                <Input
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  placeholder="…or type the code instead"
                  maxLength={7}
                />
                <Button loading={busy} onClick={() => code && join({ code })}>
                  Join
                </Button>
              </Space.Compact>
            )}
          </Space>
        )}
      </Card>
    </div>
  );
}
