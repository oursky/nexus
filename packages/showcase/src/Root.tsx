import "./index.css";
import React from "react";
import { Composition, AbsoluteFill, Sequence } from "remotion";
import { SCENES, TOTAL_FRAMES, FPS } from "./timeline";
import { DeployScene } from "./scenes/03-deploy";
import { OutroScene } from "./scenes/08-outro";

const NexusShowcase: React.FC = () => {
  return (
    <AbsoluteFill style={{ background: "#11111b" }}>
      <Sequence from={SCENES.deploy.start} durationInFrames={SCENES.deploy.duration}>
        <DeployScene />
      </Sequence>

      <Sequence from={SCENES.outro.start} durationInFrames={SCENES.outro.duration}>
        <OutroScene />
      </Sequence>
    </AbsoluteFill>
  );
};

export const RemotionRoot: React.FC = () => {
  return (
    <>
      <Composition
        id="NexusShowcase"
        component={NexusShowcase}
        durationInFrames={TOTAL_FRAMES}
        fps={FPS}
        width={1920}
        height={1080}
      />
    </>
  );
};
