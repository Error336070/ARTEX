import { fileURLToPath } from "node:url";

// Static export: `NEXT_EXPORT=1 next build` writes a static site to web/out for deployment
// in an nginx web root. Leave this variable unset for next dev to retain /api proxying and hot reload.
const isExport = process.env.NEXT_EXPORT === "1";
// Vercel demo: the entire site uses mocks without a backend or /api proxy.
const isMock = process.env.NEXT_PUBLIC_MOCK === "1";

/** @type {import('next').NextConfig} */
const nextConfig = {
  // Prevent parent-directory lockfiles from affecting root inference and asset paths.
  turbopack: { root: fileURLToPath(new URL(".", import.meta.url)) },
  reactCompiler: true,
  // Allow LAN IP addresses to access development assets (HMR); adjust as needed.
  // During development, allow any IPv4 origin to access /_next/* and HMR, including changing LAN IP addresses.
  // Next disallows a bare "*" for security. Use segment wildcards; "*.*.*.*" matches any IPv4 address.
  allowedDevOrigins: ["*.*.*.*"],
  compiler: {
    removeConsole: process.env.NODE_ENV === "production",
  },
  ...(isExport
    ? {
        // Static export: no Node runtime; images are unoptimized; each route produces <route>/index.html.
        output: "export",
        images: { unoptimized: true },
        trailingSlash: true,
      }
    : isMock
      ? {
          // Vercel mock demo: no backend or /api proxy required.
          images: { unoptimized: true },
        }
      : {
          // Development: proxy /api/* to the Go backend (default :8787, overridden by AUTOPENTEST_API).
          async rewrites() {
            const backend = process.env.AUTOPENTEST_API ?? "http://localhost:8787";
            return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
          },
        }),
};

export default nextConfig;
