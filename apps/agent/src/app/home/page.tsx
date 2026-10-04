"use client";

export default function HomePage() {
  return (
    <div className="min-h-screen bg-slate-50 p-6">
      <div className="max-w-lg mx-auto bg-white border border-slate-200 rounded-2xl p-6 shadow-sm">
        <h1 className="text-xl font-semibold text-slate-900">Home</h1>
        <p className="text-sm text-slate-500 mt-2">
          Login works. Wallet and actions come next.
        </p>
      </div>
    </div>
  );
}