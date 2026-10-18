import { Link } from 'react-router-dom'
import { PageHeader, StatusBadge } from '../components/ui'
import { useDash } from '../lib/store'
import type { DeviceType } from '../lib/types'

/**
 * Smart Devices page. Phase 1–3 ship the shell only: Hue controls arrive in
 * phase 5, Blink in phase 7, and plugs remain a "not configured" stub.
 */
const SECTIONS: { type: DeviceType; title: string; phase: string; blurb: string }[] = [
  { type: 'hue', title: 'Philips Hue', phase: 'Phase 5', blurb: 'Local Bridge API v2 — lights, sensors, battery, toggle / brightness / colour temperature.' },
  { type: 'blink', title: 'Blink cameras', phase: 'Phase 7 (experimental)', blurb: 'Online, armed, battery, temperature and last motion via the unofficial cloud API.' },
  { type: 'plug', title: 'Smart plugs', phase: 'Stub', blurb: 'Not configured. Plug control will require a confirmation dialog and a per-plug “protected” flag.' },
]

export default function SmartDevices() {
  const devices = useDash((s) => s.devices)
  const states = useDash((s) => s.states)
  return (
    <div>
      <PageHeader title="Smart Devices" />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        {SECTIONS.map((sec) => {
          const devs = devices.filter((d) => d.type === sec.type)
          return (
            <div key={sec.type} className="card">
              <div className="mb-1 flex items-center justify-between">
                <h2 className="font-semibold">{sec.title}</h2>
                <span className="rounded bg-slate-800 px-2 py-0.5 text-xs text-slate-400">{sec.phase}</span>
              </div>
              <p className="mb-4 text-sm text-slate-400">{sec.blurb}</p>
              {devs.length === 0 ? (
                <div className="rounded-lg border border-dashed border-slate-800 p-4 text-center text-sm text-slate-500">
                  Not configured. <Link to="/settings" className="text-sky-400 underline">Add a device</Link>
                </div>
              ) : (
                <ul className="space-y-2">
                  {devs.map((d) => (
                    <li key={d.id} className="flex items-center justify-between rounded-lg border border-slate-800 px-3 py-2 text-sm">
                      <span>
                        {d.name} <span className="text-xs text-slate-500">{d.host}</span>
                      </span>
                      <StatusBadge status={states[d.id]?.status} />
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
