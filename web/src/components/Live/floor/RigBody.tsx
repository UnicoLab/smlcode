import type { ReactNode } from 'react';
import type { ThreeEvent } from '@react-three/fiber';
import { HIP_Y, HEAD_Y, SHOULDER_Y, type Rig, type RigLook } from './floorRig';

// The figure's meshes; floorRig poses them. See floorRig for the local space.

type Pick = {
  onClick: (e: ThreeEvent<MouseEvent>) => void;
  onPointerOver: (e: ThreeEvent<PointerEvent>) => void;
  onPointerOut: () => void;
};

/** The figure's meshes. Everything moves through the refs in `rig`. */
export default function RigBody({ rig, look, pick, children }: { rig: Rig; look: RigLook; pick: Pick; children?: ReactNode }) {
  const opacity = look.ghost ? 0.72 : 1;
  const transparent = !!look.ghost;
  const pants = '#334155';
  return (
    <group ref={rig.root}>
      {/* Legs, on hip pivots. */}
      {([-1, 1] as const).map((side) => (
        <group key={side} ref={side < 0 ? rig.legL : rig.legR} position={[side * 0.1, HIP_Y, 0]}>
          <mesh position={[0, -0.2, 0]} castShadow>
            <capsuleGeometry args={[0.07, 0.26, 4, 8]} />
            <meshStandardMaterial color={pants} roughness={0.7} transparent={transparent} opacity={opacity} />
          </mesh>
          <mesh position={[0, -0.39, -0.05]} castShadow>
            <boxGeometry args={[0.11, 0.06, 0.18]} />
            <meshStandardMaterial color="#111827" roughness={0.6} />
          </mesh>
        </group>
      ))}
      <group ref={rig.body}>
        <mesh position={[0, 0.8, 0]} castShadow {...pick}>
          <capsuleGeometry args={[0.2, 0.38, 6, 14]} />
          <meshStandardMaterial color={look.shirt} roughness={0.55} transparent={transparent} opacity={opacity} />
        </mesh>
        {/* Arms, on shoulder pivots; the right hand holds the cup or the beer. */}
        {([-1, 1] as const).map((side) => (
          <group key={side} ref={side < 0 ? rig.armL : rig.armR} position={[side * 0.26, SHOULDER_Y, 0]}>
            <mesh position={[0, -0.19, 0]} castShadow>
              <capsuleGeometry args={[0.06, 0.24, 4, 8]} />
              <meshStandardMaterial color={look.shirt} roughness={0.55} transparent={transparent} opacity={opacity} />
            </mesh>
            <mesh position={[0, -0.36, 0]}>
              <sphereGeometry args={[0.06, 10, 10]} />
              <meshStandardMaterial color={look.skin} roughness={0.5} />
            </mesh>
            {side > 0 && (
              <>
                <group ref={rig.beer} position={[0, -0.42, -0.02]} visible={false}>
                  <mesh>
                    <cylinderGeometry args={[0.065, 0.06, 0.15, 12]} />
                    <meshStandardMaterial color="#f59e0b" emissive="#b45309" emissiveIntensity={0.35} transparent opacity={0.9} roughness={0.2} />
                  </mesh>
                  <mesh position={[0, 0.09, 0]}>
                    <cylinderGeometry args={[0.07, 0.068, 0.04, 12]} />
                    <meshStandardMaterial color="#fffbeb" roughness={0.9} />
                  </mesh>
                </group>
                <mesh ref={rig.cup} position={[0, -0.42, -0.02]} visible={false}>
                  <cylinderGeometry args={[0.05, 0.042, 0.1, 12]} />
                  <meshStandardMaterial color="#f9a8d4" roughness={0.6} />
                </mesh>
              </>
            )}
          </group>
        ))}
        <group ref={rig.head} position={[0, HEAD_Y, 0]}>
          <mesh castShadow {...pick}>
            <sphereGeometry args={[0.19, 20, 16]} />
            <meshStandardMaterial color={look.skin} roughness={0.5} transparent={transparent} opacity={opacity} />
          </mesh>
          {/* Eyes, so a turned head reads as a glance and a nap as a nap. */}
          <mesh position={[-0.06, 0.03, -0.165]}>
            <sphereGeometry args={[0.025, 8, 8]} />
            <meshBasicMaterial color="#111827" />
          </mesh>
          <mesh position={[0.06, 0.03, -0.165]}>
            <sphereGeometry args={[0.025, 8, 8]} />
            <meshBasicMaterial color="#111827" />
          </mesh>
          {look.hair && (
            <mesh position={[0, 0.035, 0.025]} castShadow>
              <sphereGeometry args={[0.205, 18, 10, 0, Math.PI * 2, 0, Math.PI / 2.3]} />
              <meshStandardMaterial color={look.hair} roughness={0.85} />
            </mesh>
          )}
          {look.crown && (
            <mesh position={[0, 0.29, 0]} rotation={[Math.PI / 2, 0, 0]}>
              <torusGeometry args={[0.13, 0.03, 10, 24]} />
              <meshStandardMaterial color="#fbbf24" emissive="#fbbf24" emissiveIntensity={0.6} metalness={0.6} roughness={0.3} />
            </mesh>
          )}
        </group>
      </group>
      {children}
    </group>
  );
}

