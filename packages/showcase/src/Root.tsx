import "./index.css";
import React from "react";
import { Composition, AbsoluteFill, Sequence } from "remotion";
import { SCENES, TOTAL_FRAMES, FPS } from "./timeline";
import { IntroScene } from "./scenes/00-intro";
import { ProblemScene } from "./scenes/01-problem";
import { ArchitectureScene } from "./scenes/02-architecture";
import { DeployScene } from "./scenes/03-deploy";
import { MacOSConnectScene } from "./scenes/04-macos-connect";
import { CreateWorkspaceScene } from "./scenes/05-create-workspace";
import { LivePreviewScene } from "./scenes/06-live-preview";
import { WorkspaceTypesScene } from "./scenes/07-workspace-types";
import { OutroScene } from "./scenes/08-outro";

const NexusShowcase: React.FC = () => {
  return (
    <AbsoluteFill style={{ background: "#11111b" }}>
      <Sequence from={SCENES.intro.start} durationInFrames={SCENES.intro.duration}>
        <IntroScene />
      </Sequence>

      <Sequence from={SCENES.problem.start} durationInFrames={SCENES.problem.duration}>
        <ProblemScene />
      </Sequence>

      <Sequence from={SCENES.architecture.start} durationInFrames={SCENES.architecture.duration}>
        <ArchitectureScene />
      </Sequence>

      <Sequence from={SCENES.deploy.start} durationInFrames={SCENES.deploy.duration}>
        <DeployScene />
      </Sequence>

      <Sequence from={SCENES.macosConnect.start} durationInFrames={SCENES.macosConnect.duration}>
        <MacOSConnectScene />
      </Sequence>

      <Sequence from={SCENES.createWorkspace.start} durationInFrames={SCENES.createWorkspace.duration}>
        <CreateWorkspaceScene />
      </Sequence>

      <Sequence from={SCENES.livePreview.start} durationInFrames={SCENES.livePreview.duration}>
        <LivePreviewScene />
      </Sequence>

      <Sequence from={SCENES.workspaceTypes.start} durationInFrames={SCENES.workspaceTypes.duration}>
        <WorkspaceTypesScene />
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
