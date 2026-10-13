"use client";

import { useEffect, useRef, useState } from "react";

import { useRouter } from "next/navigation";

import { AlertTriangle, ShieldCheck } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";

export default function LoginPage() {
  const router = useRouter();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);
  const [agreed, setAgreed] = useState(false);
  const [termsOpen, setTermsOpen] = useState(false);
  const [readToEnd, setReadToEnd] = useState(false);
  const termsBodyRef = useRef<HTMLDivElement>(null);

  // Enable agreement only after reaching the end of the terms, including when all terms fit without scrolling.
  function handleTermsScroll() {
    const el = termsBodyRef.current;
    if (!el) return;
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 8) setReadToEnd(true);
  }

  useEffect(() => {
    if (!termsOpen) return;
    // Reset on opening and handle content that fits on screen without scrolling.
    setReadToEnd(false);
    const el = termsBodyRef.current;
    if (el && el.scrollHeight <= el.clientHeight + 8) setReadToEnd(true);
  }, [termsOpen]);

  useEffect(() => {
    // Go directly to the main page when logged in; static exports have no middleware redirect.
    const token = auth.getToken();
    if (token) {
      // Credentials may remain in localStorage after the cookie is lost. Sync first, then make a fresh request
      // to prevent the server guard or route cache returning to the login page while it is still checking.
      auth.setToken(token);
      window.location.replace("/function/tasks");
      return;
    }
    api
      .authStatus()
      .then(({ initialized }) => {
        if (!initialized) router.replace("/setup");
      })
      .catch(() => setError("Cannot connect to the backend service"))
      .finally(() => setChecking(false));
  }, [router]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!agreed) {
      setError("Read and agree to the Terms of Use first");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const { token } = await api.login("ARTEX", password);
      auth.setToken(token);
      window.location.replace("/function/tasks");
    } catch {
      setError("Incorrect username or password");
    } finally {
      setLoading(false);
    }
  }

  if (checking) {
    return (
      <div role="status" className="flex min-h-dvh items-center justify-center text-muted-foreground">
        Checking login status…
      </div>
    );
  }

  return (
    <div className="flex h-dvh">
      {/* Left panel */}
      <div className="hidden flex-col items-center justify-center bg-primary p-12 text-center lg:flex lg:w-1/3">
        <div className="relative flex items-center justify-center">
          <div className="absolute size-80 rounded-full border border-primary-foreground/10" />
          <div className="absolute size-60 rounded-full border border-primary-foreground/15" />
          <div className="absolute size-40 rounded-full border border-primary-foreground/20" />
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/logo.png" alt="ARTEX" width={160} height={160} className="relative brightness-0 invert" />
        </div>
      </div>

      {/* Right panel */}
      <div className="flex w-full items-center justify-center bg-background p-8 lg:w-2/3">
        <div className="w-full max-w-md space-y-10 py-24 lg:py-32">
          <div className="space-y-4 text-center">
            <h2 className="text-2xl font-medium tracking-tight">Log in</h2>
            <p className="mx-auto max-w-xl text-muted-foreground">Welcome back. Enter your password to continue using ARTEX.</p>
          </div>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="username">Username</Label>
              <Input id="username" value="ARTEX" readOnly className="bg-muted text-muted-foreground" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="password">Password</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Enter your password"
                autoFocus
                autoComplete="current-password"
              />
            </div>
            <div className="flex items-start gap-2">
              <Checkbox
                id="agree-terms"
                checked={agreed}
                onCheckedChange={(v) => setAgreed(v === true)}
                className="mt-0.5"
              />
              <Label htmlFor="agree-terms" className="text-sm font-normal leading-relaxed text-muted-foreground">
                I have read and agree to
                <button
                  type="button"
                  onClick={() => setTermsOpen(true)}
                  className="mx-0.5 font-medium text-primary underline-offset-4 hover:underline"
                >
                  Terms of Use
                </button>
              </Label>
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading || !password || !agreed}>
              {loading ? "Logging in..." : "Log in"}
            </Button>
          </form>
        </div>
      </div>

      <Dialog open={termsOpen} onOpenChange={setTermsOpen}>
        <DialogContent className="gap-0 p-0 sm:max-w-2xl">
          <DialogHeader className="flex-row items-center gap-3 border-b px-6 py-4">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <ShieldCheck className="size-5" />
            </div>
            <div className="space-y-0.5">
              <DialogTitle className="text-base">ARTEX Terms of Use and Disclaimer</DialogTitle>
              <p className="text-xs text-muted-foreground">
                Version v1.0 · Effective 18 September 2026 · Read all terms below before logging in
              </p>
            </div>
          </DialogHeader>

          <div
            ref={termsBodyRef}
            onScroll={handleTermsScroll}
            className="max-h-[60vh] space-y-5 overflow-y-auto px-6 py-5 text-sm leading-relaxed text-muted-foreground"
          >
            <p className="rounded-lg border bg-muted/40 p-3 text-foreground/80">
              These Terms of Use and Disclaimer (the “Statement”) form an agreement between you and the ARTEX
              project authors and contributors concerning use of this software. Read and understand all terms before use, particularly the disclaimers, limits of liability, and prohibitions highlighted in bold or coloured blocks.
              <span className="font-medium text-foreground">
                {" "}
                By downloading, installing, accessing, or otherwise using this software, you are deemed to have read, understood, and agreed to be bound by all terms of this Statement.
              </span>
            </p>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  1
                </span>
                Article 1 · Definitions and open-source licence
              </h4>
              <p className="pl-7">
                This software (ARTEX) is an open-source program released under the GNU Affero General Public License
                v3.0 (AGPL-3.0). You may use, copy, modify, and distribute it under that licence. Any derivative work, including online services provided to third parties over a network, must also be released under
                AGPL-3.0 with the complete corresponding source code made available to users. The accompanying LICENSE file contains the full AGPL-3.0 terms.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  2
                </span>
                Article 2 · Permitted use
              </h4>
              <p className="pl-7">
                This software is solely for personal learning, code research, discussion of security principles, and technical verification in a local isolated environment you build yourself. It is intended for non-offensive, non-destructive uses such as learning, academic research, and code review. You may not use it for any purpose other than those expressly permitted in this article.
              </p>
            </section>

            <section className="space-y-2">
              <h4 className="flex items-center gap-2 font-medium text-destructive">
                <span className="flex size-5 items-center justify-center rounded-md bg-destructive/10 text-xs font-semibold text-destructive">
                  3
                </span>
                <AlertTriangle className="size-4" />
                Article 3 · Prohibited conduct
              </h4>
              <ul className="ml-7 list-decimal space-y-1.5 rounded-lg border border-destructive/20 bg-destructive/5 p-3 pl-8 text-foreground/80 marker:text-destructive/70">
                <li>
                  Scanning, probing, exploiting, or attacking any website, online service, or networked system belonging to others or third parties is strictly prohibited, regardless of authorisation or ownership of the assets;
                </li>
                <li>Using this software for actual penetration testing, offensive or defensive engagements, red/blue team exercises, or production environments is strictly prohibited;</li>
                <li>Using this software for unlawful intrusion, data theft, extortion, denial of service (DoS/DDoS), or any destructive or criminal activity is strictly prohibited;</li>
                <li>Removing, altering, or bypassing any copyright, licence, or safety notice in the software or its output is strictly prohibited;</li>
                <li>Any conduct that violates the laws, regulations, or regulatory requirements of your country or region is strictly prohibited.</li>
              </ul>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  4
                </span>
                Article 4 · Intellectual property
              </h4>
              <p className="pl-7">
                The project authors and contributors own the copyright and related intellectual property rights in this software. You are granted rights within the scope of
                AGPL-3.0. Apart from the rights expressly granted by that licence, this Statement grants you no other rights, expressly or implicitly.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  5
                </span>
                Article 5 · Data and privacy
              </h4>
              <p className="pl-7">
                This software is open source and can be deployed by you. The authors operate no centralised service and do not collect or upload your usage data. You control all data generated, processed, or encountered during use and are responsible for its legality and security. You bear all consequences of improper data handling.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  6
                </span>
                Article 6 · Compliance and legal responsibility
              </h4>
              <p className="pl-7">
                You must comply with all laws and regulations in your country or region concerning cybersecurity, data security, personal information protection, computer crime, and related matters. In mainland China, these include the Cybersecurity Law, Data Security Law, Personal Information Protection Law, and relevant judicial interpretations.
                <span className="font-medium text-foreground">
                  {" "}
                  You alone bear all legal responsibility and consequences arising from violations of those laws and regulations or this Statement. The software authors and contributors bear no responsibility for them.
                </span>
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  7
                </span>
                Article 7 · Disclaimer and limitation of liability
              </h4>
              <p className="pl-7">
                This software is provided “AS IS” and “AS
                AVAILABLE”, without express or implied warranties, including warranties of merchantability, fitness for a particular purpose, accuracy, or non-infringement. To the fullest extent permitted by applicable law, the authors and contributors are not liable for any direct, indirect, incidental, special, or consequential losses arising from use of or inability to use the software, whether used properly or otherwise. These include data loss, system damage, business interruption, lost profits, and legal disputes.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  8
                </span>
                Article 8 · Changes to terms and interpretation
              </h4>
              <p className="pl-7">
                The authors may update this Statement as required by law or project development. Updated versions will be released with the project and take effect on publication. Continued use constitutes acceptance of the revised terms. To the extent permitted by law, the project authors retain the right of final interpretation. If any provision is held invalid, the remaining provisions remain in effect.
              </p>
            </section>
          </div>

          <DialogFooter className="mx-0 mb-0 flex-col items-stretch gap-2 rounded-b-xl px-6 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-xs text-muted-foreground">
              {readToEnd ? "You have viewed all terms" : "Scroll to the end of the terms before confirming"}
            </p>
            <DialogClose asChild>
              <Button
                type="button"
                disabled={!readToEnd}
                onClick={() => {
                  setAgreed(true);
                  setError("");
                }}
              >
                I have read and agree to all terms
              </Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
